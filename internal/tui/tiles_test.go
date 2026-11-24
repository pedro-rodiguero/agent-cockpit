package tui

import (
	"testing"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/chat"
	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
)

func keys(tiles []tile) []string {
	var k []string
	for _, t := range tiles {
		k = append(k, t.key)
	}
	return k
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBuildTilesMergesPaneAndSessionByPID(t *testing.T) {
	sessions := []model.Session{
		{ID: "a", PID: 10, State: model.Busy},
		{ID: "b", PID: 20, State: model.Idle},
	}
	panes := []mux.PaneInfo{{ID: "p1", PID: 20}, {ID: "p2", PID: 99}}
	tiles := buildTiles(sessions, panes, listView, time.Now())
	if got := keys(tiles); !equal(got, []string{"pane:p1", "pane:p2", "a"}) {
		t.Fatalf("tiles = %v", got)
	}
	if tiles[0].sess == nil || tiles[0].sess.ID != "b" {
		t.Fatal("pane p1 not merged with session b")
	}
	if tiles[1].sess != nil {
		t.Fatal("pane p2 merged with a session it does not run")
	}
}

func TestBuildTilesGridOrder(t *testing.T) {
	t0 := time.Now()
	sessions := []model.Session{
		{ID: "inbox", State: model.Inbox, Started: t0.Add(-time.Hour)},
		{ID: "new", State: model.Waiting, Started: t0},
		{ID: "old", State: model.Idle, Started: t0.Add(-time.Minute), Since: t0.Add(-time.Minute)},
		{ID: "stale", State: model.Idle, Started: t0.Add(-9 * time.Hour), Since: t0.Add(-5 * time.Hour)},
		{ID: "stale2", State: model.Idle, Started: t0.Add(-8 * time.Hour), Since: t0.Add(-2 * time.Hour)},
	}
	panes := []mux.PaneInfo{{ID: "gone", Exited: true}, {ID: "live"}}
	tiles := buildTiles(sessions, panes, gridView, t0)
	want := []string{"pane:live", "old", "new", "inbox", "pane:gone", "stale2", "stale"}
	if got := keys(tiles); !equal(got, want) {
		t.Errorf("grid order = %v, want %v", got, want)
	}
	if n := railCount(tiles, t0); n != 4 {
		t.Errorf("railCount = %d, want 4", n)
	}
	if got := keys(buildTiles(sessions[:3], nil, listView, t0)); !equal(got, []string{"inbox", "new", "old"}) {
		t.Errorf("list order = %v, want the collector's", got)
	}
}

func TestNeedingAttention(t *testing.T) {
	sessions := []model.Session{
		{ID: "idle", State: model.Idle},
		{ID: "wait", State: model.Waiting},
		{ID: "typing", State: model.Busy},
	}
	tiles := buildTiles(sessions, nil, listView, time.Now())
	if got := keys(needingAttention(tiles, "")); !equal(got, []string{"wait"}) {
		t.Errorf("got %v", got)
	}
	if got := keys(needingAttention(tiles, "typing")); !equal(got, []string{"wait", "typing"}) {
		t.Errorf("kept tile missing: %v", got)
	}
}

func TestTileCapabilities(t *testing.T) {
	claude := model.Session{Agent: "claude", Socket: "/s", NativeID: "n", PID: 1, Cwd: "/x", TTY: "ttys004"}
	codexNoID := model.Session{Agent: "codex", PID: 1, Cwd: "/x"}
	pane := mux.PaneInfo{ID: "p"}
	cases := []struct {
		name           string
		t              tile
		reply, canMove bool
	}{
		{"claude in a tab", tile{sess: &claude}, true, true},
		{"claude in a pane", tile{sess: &claude, pane: &pane}, true, false},
		{"codex without id", tile{sess: &codexNoID}, false, false},
	}
	for _, c := range cases {
		if c.t.canReply() != c.reply || c.t.canAdopt() != c.canMove {
			t.Errorf("%s: canReply %v canAdopt %v", c.name, c.t.canReply(), c.t.canAdopt())
		}
	}
	if h := tileKeys(tile{sess: &codexNoID}); len(h) != 1 || h[0].key != "z" {
		t.Errorf("keys offer what the card can't do: %v", h)
	}
	if h := tileKeys(tile{sess: &claude}); len(h) != 4 || h[0] != (keyHint{"⏎", "continue here"}) {
		t.Errorf("claude in a tab: %v", h)
	}
}

func TestLabelDiff(t *testing.T) {
	shown := map[string]string{"s1": "A", "s2": "B"}
	want := map[string]string{"s1": "A", "s3": "C"}
	changes, next := labelDiff(shown, want)
	if len(changes) != 2 || changes["s2"] != "" || changes["s3"] != "C" {
		t.Errorf("changes = %v", changes)
	}
	if _, ok := changes["s2"]; !ok {
		t.Error("removed tab not cleared")
	}
	if len(next) != 2 || next["s1"] != "A" || next["s3"] != "C" {
		t.Errorf("next = %v", next)
	}
	cleared, after := labelDiff(shown, nil)
	if len(cleared) != 2 || cleared["s1"] != "" || len(after) != 0 {
		t.Errorf("clear all = %v, %v", cleared, after)
	}
}

func TestMarkAsked(t *testing.T) {
	question := chat.Conversation{Lines: []chat.Line{{Kind: chat.Assistant, Text: "Should I push?"}}}
	snap := model.Snapshot{Sessions: []model.Session{
		{ID: "a", State: model.Idle},
		{ID: "b", State: model.Idle},
		{ID: "c", State: model.Busy},
	}}
	chats := map[string]chat.Conversation{"b": question, "c": question}
	got := markAsked(snap, chats).Sessions
	for i, want := range []model.State{model.Idle, model.Asked, model.Busy} {
		if got[i].State != want {
			t.Errorf("session %s: state %s, want %s", got[i].ID, got[i].State, want)
		}
	}
}

func TestTileName(t *testing.T) {
	for _, tc := range []struct {
		t    tile
		want string
	}{
		{tile{sess: &model.Session{Name: "saas-2f", Title: "Job board for the States", State: model.Idle}}, "Job board for the States"},
		{tile{sess: &model.Session{Name: "saas-2f", State: model.Idle}}, "saas-2f"},
		{tile{sess: &model.Session{Name: "payments", Title: "Retry failed webhooks", State: model.Inbox}}, "payments"},
		{tile{pane: &mux.PaneInfo{Cwd: "/srv/api"}}, "api"},
	} {
		if got := tc.t.name(); got != tc.want {
			t.Errorf("name() = %q, want %q", got, tc.want)
		}
	}
}

func TestKeysWithoutTabs(t *testing.T) {
	canFocusTabs = false
	defer func() { canFocusTabs = true }()
	s := model.Session{Agent: "claude", PID: 1, Cwd: "/x", NativeID: "n", TTY: "pts/3"}
	for _, k := range tileKeys(tile{sess: &s}) {
		if k.key == "o" || k.does == "go to tab" {
			t.Errorf("offers a tab it cannot reach: %v", tileKeys(tile{sess: &s}))
		}
	}
}
