package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/chat"
	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

func plain(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.TrimRight(ansi.Strip(r), " ")
	}
	return out
}

func sessionTile(s model.Session) tile { return tile{key: s.ID, sess: &s} }

func TestTokens(t *testing.T) {
	for n, want := range map[int]string{950: "950", 1499: "1k", 36831: "37k", 317108: "317k", 1_250_000: "1.2M"} {
		if got := tokens(n); got != want {
			t.Errorf("tokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestContextSize(t *testing.T) {
	if s, _ := contextSize(chat.Info{}); s != "" {
		t.Errorf("no tokens: %q", s)
	}
	if s, fg := contextSize(chat.Info{ContextTokens: 317108}); s != "317k ctx" || fg != cMuted {
		t.Errorf("unknown window: %q %v", s, fg)
	}
	if s, fg := contextSize(chat.Info{ContextTokens: 400000, ContextWindow: 475000}); s != "400k/475k ctx" || fg != cWaiting {
		t.Errorf("nearly full: %q %v", s, fg)
	}
}

func TestContextBarShedsFieldsAsItNarrows(t *testing.T) {
	tl := sessionTile(model.Session{ID: "s", Cwd: "/srv/projects/customer-portal"})
	info := chat.Info{Model: "opus-5-5", Branch: "feat/limits", ContextTokens: 212000}
	cases := []struct {
		w    int
		want string
	}{
		{80, " /srv/projects/customer-portal  ⎇ feat/limits" + strings.Repeat(" ", 15) + "opus-5-5 · 212k ctx"},
		{60, " …jects/customer-portal  ⎇ feat/limits  opus-5-5 · 212k ctx"}, // the directory gives way first
		{44, " …/customer-portal  ⎇ feat/limits  212k ctx"},                 // then the model
		{30, " …s/customer-portal  212k ctx"},                               // then the branch
		{16, " …stomer-portal"},                                             // then the context size
	}
	for _, c := range cases {
		row := contextBar(tl, info, c.w)
		if width(row) != c.w {
			t.Errorf("w=%d: row is %d cells wide", c.w, width(row))
		}
		if got := strings.TrimRight(ansi.Strip(row), " "); got != c.want {
			t.Errorf("w=%d:\n got %q\nwant %q", c.w, got, c.want)
		}
	}
}

func TestStateBarPrefersTheRunningToolOverModeAndEffort(t *testing.T) {
	now := time.Now()
	busy := sessionTile(model.Session{ID: "s", State: model.Busy, Since: now.Add(-2 * time.Minute)})
	info := chat.Info{Activity: "Bash npm run test -- auth", Running: 3, Mode: "plan", Effort: "high"}
	if got := plain([]string{stateBar(busy, info, 70, now, false)})[0]; got != " ● working 2m · Bash npm run test -- auth  +2 more"+strings.Repeat(" ", 8)+"plan · high" {
		t.Errorf("wide: %q", got)
	}
	if got := plain([]string{stateBar(busy, info, 54, now, false)})[0]; got != " ● working 2m · Bash npm run test -- auth  +2 more" {
		t.Errorf("narrow keeps the activity whole: %q", got)
	}
	waiting := sessionTile(model.Session{ID: "s", State: model.Waiting, Detail: "permission prompt", Since: now.Add(-30 * time.Second)})
	if got := plain([]string{stateBar(waiting, chat.Info{}, 60, now, false)})[0]; got != " ▲ waiting 30s · permission prompt" {
		t.Errorf("waiting: %q", got)
	}
}

func TestToolCalls(t *testing.T) {
	if toolCalls(1) != "1 tool call" || toolCalls(3) != "3 tool calls" {
		t.Errorf("toolCalls: %q %q", toolCalls(1), toolCalls(3))
	}
}

func TestFoldToolRuns(t *testing.T) {
	tool := func(s string) chat.Line { return chat.Line{Kind: chat.Tool, Text: s} }
	in := []chat.Line{{Kind: chat.User, Text: "go"}, tool("a"), tool("b"), tool("c"), tool("d"), tool("e"),
		{Kind: chat.Assistant, Text: "done"}, tool("f"), tool("g")}
	var got []string
	for _, l := range foldToolRuns(in) {
		got = append(got, l.Text)
	}
	if want := "go|3 tool calls|d|e|done|f|g"; strings.Join(got, "|") != want {
		t.Errorf("got %s, want %s", strings.Join(got, "|"), want)
	}
}

func TestSessionCardPinsScrolledOutPrompt(t *testing.T) {
	a := newTestApp(gridView, 120, 40)
	s := model.Session{ID: "s", Agent: "claude", Cwd: "/srv/api", Title: "Rate limiting", State: model.Idle}
	long := strings.Repeat("word ", 60)
	a.convs = map[string]chat.Conversation{"s": {Lines: []chat.Line{
		{Kind: chat.User, Text: "add rate limiting"},
		{Kind: chat.Assistant, Text: long},
	}}}
	rows := plain(a.sessionContent(sessionTile(s), 40, 12))
	if len(rows) != 12 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0] != " /srv/api" || rows[1] != " › add rate limiting" {
		t.Errorf("head rows: %q", rows[:2])
	}
	if !strings.HasPrefix(rows[11], " ○ idle") {
		t.Errorf("state bar: %q", rows[11])
	}

	// A short conversation still shows its prompt in place, so it is not pinned a second time.
	a.convs["s"] = chat.Conversation{Lines: []chat.Line{{Kind: chat.User, Text: "add rate limiting"}, {Kind: chat.Assistant, Text: "Done."}}}
	rows = plain(a.sessionContent(sessionTile(s), 40, 12))
	if n := strings.Count(strings.Join(rows, "\n"), "› add rate limiting"); n != 1 {
		t.Errorf("prompt shown %d times: %q", n, rows)
	}

	// A card too short for the extras keeps its rows for the conversation.
	a.convs["s"] = chat.Conversation{Lines: []chat.Line{{Kind: chat.User, Text: "add rate limiting"}, {Kind: chat.Assistant, Text: long}}}
	rows = plain(a.sessionContent(sessionTile(s), 40, 6))
	if rows[1] == " › add rate limiting" {
		t.Errorf("short card spent a row on the pinned prompt: %q", rows)
	}
}

func TestIdleCardShowsLastReplyFromItsStart(t *testing.T) {
	a := newTestApp(gridView, 120, 40)
	s := model.Session{ID: "s", Agent: "claude", Cwd: "/srv/api", Title: "Rate limiting", State: model.Asked}
	reply := []chat.Line{{Kind: chat.User, Text: "add rate limiting"}, {Kind: chat.Tool, Text: "Edit limits.ts"}}
	for i := 1; i <= 8; i++ {
		reply = append(reply, chat.Line{Kind: chat.Assistant, Text: fmt.Sprintf("paragraph %d", i)})
	}
	reply = append(reply, chat.Line{Kind: chat.Assistant, Text: "Should I push?"})
	a.convs = map[string]chat.Conversation{"s": {Lines: reply}}
	rows := plain(a.sessionContent(sessionTile(s), 40, 8))
	want := []string{" /srv/api", " › add rate limiting", " paragraph 1", " paragraph 2", " paragraph 3", " ⋯", " Should I push?"}
	if strings.Join(rows[:7], "|") != strings.Join(want, "|") {
		t.Errorf("rows:\n%q\nwant:\n%q", rows[:7], want)
	}

	// A working session shows the end of its conversation, where things happen.
	s.State = model.Busy
	rows = plain(a.sessionContent(sessionTile(s), 40, 8))
	if rows[6] != " Should I push?" || rows[5] != " paragraph 8" {
		t.Errorf("busy rows: %q", rows)
	}
}

func TestPanesSeeThroughATranslucentWindow(t *testing.T) {
	defer useTheme(appearance{})
	plain := "\x1b[0;38;2;233;223;201;48;2;20;17;12mhi\x1b[0;38;2;0;0;0;48;2;201;165;78mX\x1b[0m"
	if got := seeThrough(plain); got != plain {
		t.Fatalf("opaque window changed: %q", got)
	}
	useTheme(appearance{terminalBg: true})
	want := "\x1b[0;38;2;233;223;201mhi\x1b[0;38;2;0;0;0;48;2;201;165;78mX\x1b[0m"
	if got := seeThrough(plain); got != want {
		t.Fatalf("translucent: %q", got)
	}
	useTheme(appearance{terminalBg: true, light: true})
	if got := seeThrough(plain); got != plain {
		t.Fatalf("light theme changed: %q", got)
	}
}
