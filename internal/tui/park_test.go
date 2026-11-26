package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
)

func TestParseSSH(t *testing.T) {
	for in, want := range map[string]sshCommand{
		"ssh prod":                                 {host: "prod"},
		"ssh -N -L 5432:db:5432 deploy@bastion":    {host: "bastion", tunnel: true},
		"/usr/bin/ssh -p 2222 -i ~/.ssh/k host ls": {host: "host"},
		"ssh -NfL 8080:web:80 jump":                {host: "jump", tunnel: true},
		"ssh -o ProxyJump=a -J b -t box":           {host: "box"},
		"autossh -M 0 -N -D 1080 socks":            {host: "socks", tunnel: true},
		"mosh --ssh=ssh -p 60001 me@devbox":        {host: "devbox"},
		"ssh -- weird-host":                        {host: "weird-host"},
	} {
		got, ok := parseSSH(in)
		if !ok || got != want {
			t.Errorf("%q: %+v %v, want %+v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "vim notes.md", "ssh", "ssh -p 22", "sshd -D"} {
		if c, ok := parseSSH(in); ok {
			t.Errorf("%q read as ssh: %+v", in, c)
		}
	}
}

// parkApp is a grid with two shell panes: a tunnel and an interactive login, both quiet since t0.
func parkApp(t *testing.T, t0 time.Time) (*App, *fakeServer) {
	t.Helper()
	a, fs := appWithPane()
	fs.panes = []mux.PaneInfo{
		{ID: "p1", Agent: "shell", Cwd: "/tmp", PID: 10, Started: t0, LastIO: t0, Foreground: "ssh -N -L 5432:db:5432 bastion"},
		{ID: "p2", Agent: "shell", Cwd: "/tmp", PID: 11, Started: t0, LastIO: t0, Foreground: "ssh prod"},
	}
	a.snap.Sessions = nil
	a.rebuild()
	a.sel = paneKey("p1")
	return a, fs
}

func parkedKeys(a *App) []string {
	var out []string
	for _, tl := range a.tiles {
		if tl.parked {
			out = append(out, tl.key)
		}
	}
	return out
}

func TestQuietTunnelIsOfferedTheRail(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	a, fs := parkApp(t, t0)

	a.tendParking(t0.Add(30 * time.Second))
	if a.prompt != nil {
		t.Fatalf("asked before a minute of quiet: %q", a.prompt.label)
	}
	a.tendParking(t0.Add(2 * time.Minute))
	if a.prompt == nil || !strings.Contains(a.prompt.label, "ssh tunnel to bastion") || !strings.Contains(a.prompt.label, "a always move tunnels") {
		t.Fatalf("no question about the tunnel: %+v", a.prompt)
	}
	press(a, "a")
	if got := parkedKeys(a); len(got) != 1 || got[0] != paneKey("p1") || !a.park.AlwaysTunnels {
		t.Fatalf("parked %v always %v", got, a.park.AlwaysTunnels)
	}
	if p := a.tiles[a.index(paneKey("p1"))].place(t0); p != inParked {
		t.Fatalf("place %v", p)
	}

	// A new tunnel moves on its own once quiet; the login is not asked about before ten minutes.
	fs.panes = append(fs.panes, mux.PaneInfo{ID: "p3", Agent: "shell", PID: 12, Started: t0, LastIO: t0, Foreground: "ssh -N -D 1080 socks"})
	a.rebuild()
	a.tendParking(t0.Add(5 * time.Minute))
	if got := parkedKeys(a); len(got) != 2 || a.prompt != nil {
		t.Fatalf("parked %v prompt %+v", got, a.prompt)
	}

	// The choice survives a restart.
	b, bfs := appWithPane()
	b.park = loadParking(a.park.path)
	bfs.panes = fs.panes
	b.snap.Sessions = nil
	b.rebuild()
	if got := parkedKeys(b); len(got) != 2 || !b.park.AlwaysTunnels {
		t.Fatalf("after a restart: parked %v", got)
	}
}

func TestQuietLoginIsAskedAboutAfterTenMinutes(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	a, _ := parkApp(t, t0)
	a.park.Declined[paneIdentity(a.panes[0])] = true // the tunnel is settled; this is about the login

	a.tendParking(t0.Add(5 * time.Minute))
	if a.prompt != nil {
		t.Fatalf("asked after 5 minutes: %q", a.prompt.label)
	}
	a.tendParking(t0.Add(11 * time.Minute))
	if a.prompt == nil || !strings.Contains(a.prompt.label, "ssh to prod") || !strings.Contains(a.prompt.label, "a always for prod") {
		t.Fatalf("question %+v", a.prompt)
	}

	// Enter is not an answer: the question waits, and the key does what it does (here, nothing harmful).
	press(a, "\r")
	if len(parkedKeys(a)) != 0 || a.park.Declined[paneIdentity(a.panes[1])] {
		t.Fatal("enter answered the question")
	}
	a.mode = navMode
	a.tendParking(t0.Add(12 * time.Minute))
	if a.prompt != nil {
		t.Fatal("asked again right after the question was passed over")
	}

	// Asked again later; n keeps it in the grid for good.
	a.parkSnooze = map[string]time.Time{}
	a.tendParking(t0.Add(30 * time.Minute))
	press(a, "n")
	a.tendParking(t0.Add(50 * time.Minute))
	if a.prompt != nil || len(parkedKeys(a)) != 0 {
		t.Fatalf("after n: prompt %+v parked %v", a.prompt, parkedKeys(a))
	}
}

// Any other key passes the question over and still moves the selection.
func TestParkingQuestionDoesNotSwallowNavigation(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	a, _ := parkApp(t, t0)
	a.tendParking(t0.Add(2 * time.Minute))
	if a.prompt == nil {
		t.Fatal("no question")
	}
	before := a.sel
	press(a, "l")
	if a.prompt != nil || a.sel == before {
		t.Fatalf("l: prompt %+v selection %s → %s", a.prompt, before, a.sel)
	}
}

func TestParkedPaneComesBack(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	a, fs := parkApp(t, t0)
	a.tendParking(t0.Add(2 * time.Minute))
	press(a, "y")
	if len(parkedKeys(a)) != 1 {
		t.Fatal("y did not park")
	}

	lines, _ := a.compose()
	screen := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(screen, "PARKED") || !strings.Contains(screen, "ssh -N -L 5432:db:5432") {
		t.Fatalf("rail:\n%s", screen)
	}

	// ⏎ on it brings it back and types into it.
	a.sel = paneKey("p1")
	press(a, "\r")
	if len(parkedKeys(a)) != 0 || a.mode != insertMode {
		t.Fatalf("enter: parked %v mode %v", parkedKeys(a), a.mode)
	}

	// Parked again, it comes back on its own once the connection ends.
	a.mode = navMode
	a.park.Parked[paneIdentity(a.panes[0])] = true
	fs.panes[0].Foreground = ""
	a.rebuild()
	a.tendParking(t0.Add(3 * time.Minute))
	if len(parkedKeys(a)) != 0 {
		t.Fatal("a pane whose ssh ended stayed parked")
	}
}

// paneGrid is a 160×40 grid of n panes and nothing else, p1 selected.
func paneGrid(t *testing.T, n int) (*App, *fakeServer) {
	t.Helper()
	a, fs := appWithPane()
	now := time.Now()
	fs.panes = nil
	for i := 1; i <= n; i++ {
		fs.panes = append(fs.panes, mux.PaneInfo{ID: "p" + string(rune('0'+i)), Agent: "shell", Cwd: "/tmp", PID: 100 + i, Started: now, LastIO: now})
	}
	a.snap.Sessions = nil
	a.rebuild()
	a.sel = paneKey("p1")
	a.relayout()
	return a, fs
}

// A pane nobody typed into and that printed nothing for quietAfter goes to the rail, unless it needs
// the person; one brought back stays a while.
func TestQuietPaneGoesToTheRail(t *testing.T) {
	a, fs := paneGrid(t, 2)
	old := time.Now().Add(-2 * quietAfter)
	fs.panes[0].Started, fs.panes[0].LastIO = old, old
	a.rebuild()
	a.relayout()
	i := a.index(paneKey("p1"))
	if a.railN != 1 || a.tiles[i].place(time.Now()) != inQuiet {
		t.Fatalf("rail %d, place %v", a.railN, a.tiles[i].place(time.Now()))
	}
	if lines, _ := a.compose(); len(lines) == 0 {
		t.Fatal("nothing drawn")
	}

	// It comes back while its agent waits on the person.
	a.snap.Sessions = []model.Session{{ID: "s1", Agent: "claude", PID: fs.panes[0].PID, State: model.Waiting, Since: old}}
	a.rebuild()
	if a.railN != 0 {
		t.Fatal("a waiting pane stayed in the rail")
	}
	a.snap.Sessions = nil
	a.rebuild()

	// ⏎ on it brings it back for another quietAfter.
	a.sel = paneKey("p1")
	press(a, "\r")
	if a.railN != 0 || a.mode != insertMode {
		t.Fatalf("rail %d mode %v", a.railN, a.mode)
	}
}

func TestSetAPaneAside(t *testing.T) {
	a, _ := paneGrid(t, 3)
	a.sel = paneKey("p2")
	press(a, "s")
	i := a.index(paneKey("p2"))
	if a.tiles[i].place(time.Now()) != inParked || !a.park.Aside[paneIdentity(*a.tiles[i].pane)] {
		t.Fatalf("place %v", a.tiles[i].place(time.Now()))
	}
	// The SSH rules do not bring back a pane set aside by hand.
	a.tendParking(time.Now())
	if a.railN != 1 {
		t.Fatal("tendParking brought it back")
	}
	press(a, "s")
	if a.railN != 0 || len(a.park.Aside) != 0 {
		t.Fatalf("still aside: rail %d", a.railN)
	}
}
