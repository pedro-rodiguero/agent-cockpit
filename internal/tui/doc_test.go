package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/chat"
	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
)

// planApp is a list with one Claude session whose plan file is at path.
func planApp(t *testing.T) (*App, string) {
	cfg := t.TempDir()
	path := filepath.Join(cfg, "plans", "steady-crafting-grove.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(listView, 120, 30)
	a.snap.Sessions = []model.Session{{ID: "s1", Agent: "claude", Name: "api", State: model.Waiting,
		SessionsDir: filepath.Join(cfg, "sessions"), Socket: "/nonexistent.sock"}}
	a.convs = map[string]chat.Conversation{"s1": {Info: chat.Info{Plan: "steady-crafting-grove"}}}
	a.rebuild()
	a.sel = "s1"
	return a, path
}

func screenText(a *App) string {
	lines, _ := a.compose()
	return strings.Join(mdPlain(lines), "\n")
}

func TestPlanViewer(t *testing.T) {
	a, path := planApp(t)
	var b strings.Builder
	b.WriteString("# The plan\n\n")
	for i := 1; i <= 40; i++ {
		b.WriteString("Step line " + itoa(i) + "\n\n")
		if i == 10 {
			b.WriteString("## Rollout\n\n")
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	press(a, "m")
	if a.doc == nil {
		t.Fatalf("not opened: %q", a.flash.text)
	}
	s := screenText(a)
	if !strings.Contains(s, "PLAN · API") || !strings.Contains(s, "The plan") || strings.Contains(s, "Step line 40") {
		t.Fatalf("first screen:\n%s", s)
	}

	press(a, "]")
	if s := screenText(a); !strings.Contains(strings.SplitN(s, "\n", 6)[4], "Rollout") {
		t.Fatalf("] did not bring the next heading to the top:\n%s", s)
	}
	press(a, "[")
	if a.doc.scroll != 0 {
		t.Fatalf("[ went to line %d", a.doc.scroll)
	}
	press(a, "G")
	if s := screenText(a); !strings.Contains(s, "Step line 40") {
		t.Fatalf("G:\n%s", s)
	}

	// The agent rewrites the plan: the viewer shows the new text without being reopened.
	if err := os.WriteFile(path, []byte("# Rewritten\n\nShorter now.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := screenText(a); !strings.Contains(s, "Shorter now.") || !strings.Contains(s, "updated") {
		t.Fatalf("after a rewrite:\n%s", s)
	}

	press(a, "r")
	if a.mode != promptMode || a.prompt.kind != promptFeedback || a.prompt.target.key != "s1" {
		t.Fatalf("r: mode %v prompt %+v", a.mode, a.prompt)
	}
	a.onKeys([]key{{code: keyEsc}})
	if a.doc == nil {
		t.Fatal("cancelling the feedback closed the viewer")
	}
	press(a, "q")
	if a.doc != nil || a.quit {
		t.Fatalf("q: doc %v quit %v", a.doc, a.quit)
	}
}

func TestPlanViewerWithoutAPlan(t *testing.T) {
	a, path := planApp(t)
	// Claude Code names every session: a name alone is no plan.
	press(a, "m")
	if a.doc != nil || !strings.Contains(a.flash.text, "no plan yet") {
		t.Fatalf("no file: doc %v flash %q", a.doc, a.flash.text)
	}
	if strings.Contains(a.footerKeys(a.w, func(k, d string) string { return k + " " + d + " " }), "plan") {
		t.Fatal("the footer offers a plan that does not exist")
	}
	for _, evil := range []string{"../../.ssh/notes", "a/b", "..", "x\x1b[2J"} {
		a.convs["s1"] = chat.Conversation{Info: chat.Info{Plan: evil, Mode: "plan"}}
		press(a, "m")
		if a.doc != nil || !strings.Contains(a.flash.text, "not a plan name") {
			t.Fatalf("slug %q: doc %v flash %q", evil, a.doc, a.flash.text)
		}
	}
	a.convs["s1"] = chat.Conversation{}
	press(a, "m")
	if a.doc != nil || !strings.Contains(a.flash.text, "no plan yet") {
		t.Fatalf("no slug: doc %v flash %q", a.doc, a.flash.text)
	}

	// In plan mode the viewer opens before the plan exists and shows it once Claude writes it.
	a.convs["s1"] = chat.Conversation{Info: chat.Info{Plan: "steady-crafting-grove", Mode: "plan"}}
	if !strings.Contains(a.footerKeys(a.w, func(k, d string) string { return k + " " + d + " " }), "plan") {
		t.Fatal("no plan hint for a session in plan mode")
	}
	press(a, "m")
	if a.doc == nil || !strings.Contains(screenText(a), "waiting for the plan") {
		t.Fatalf("plan mode: doc %v flash %q", a.doc, a.flash.text)
	}
	if err := os.WriteFile(path, []byte("# Ship it\n\nfirst step\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := screenText(a); !strings.Contains(got, "Ship it") || strings.Contains(got, "waiting for the plan") {
		t.Fatalf("after the write:\n%s", got)
	}
}

// Typing into a pane, m goes to the agent: a plan it writes or changes is pointed out in the footer.
func TestPlanChangeInAPaneIsPointedOut(t *testing.T) {
	a, _ := paneWithPlan(t, "plan ready")
	press(a, "q") // close the viewer the helper opened
	a.mode = insertMode
	path, _ := a.planFile(a.tiles[a.index(paneKey("p1"))])
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	a.notePlan(now)
	if !strings.Contains(a.flash.text, "ctrl-q then m") {
		t.Fatalf("a new plan was not pointed out: %q", a.flash.text)
	}
	a.flash = flash{}
	a.notePlan(now)
	if a.flash.text != "" {
		t.Fatalf("an unchanged plan was pointed out again: %q", a.flash.text)
	}
	later := now.Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	a.notePlan(later)
	if !strings.Contains(a.flash.text, "the plan changed") {
		t.Fatalf("a changed plan was not pointed out: %q", a.flash.text)
	}
}

func TestFeedbackMessageNamesThePlan(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := feedbackMessage(filepath.Join(home, ".claude", "plans", "x.md"), "split step 2")
	if got != "Feedback on your plan (~/.claude/plans/x.md):\n\nsplit step 2" {
		t.Fatalf("%q", got)
	}
}

// plansDirectory moves plans inside the project; the local settings win over the project's, which win
// over the profile's. A folder outside the project is ignored, as Claude Code ignores it.
func TestPlanFollowsPlansDirectory(t *testing.T) {
	a, defaultPath := planApp(t)
	proj := t.TempDir()
	a.snap.Sessions[0].Cwd = proj
	a.rebuild()
	cfg := filepath.Dir(filepath.Dir(defaultPath))
	put := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opened := func() string {
		t.Helper()
		a.doc = nil
		press(a, "m")
		if a.doc == nil {
			t.Fatalf("not opened: %q", a.flash.text)
		}
		return a.doc.path
	}
	put(defaultPath, "# default")

	put(filepath.Join(cfg, "settings.json"), `{"plansDirectory": "user-plans"}`)
	put(filepath.Join(proj, "user-plans", "steady-crafting-grove.md"), "# user")
	if got := opened(); got != filepath.Join(proj, "user-plans", "steady-crafting-grove.md") {
		t.Fatalf("user setting: %s", got)
	}

	put(filepath.Join(proj, ".claude", "settings.json"), `{"plansDirectory": "docs/plans"}`)
	put(filepath.Join(proj, "docs", "plans", "steady-crafting-grove.md"), "# project")
	if got := opened(); got != filepath.Join(proj, "docs", "plans", "steady-crafting-grove.md") {
		t.Fatalf("project setting: %s", got)
	}

	put(filepath.Join(proj, ".claude", "settings.local.json"), `{"plansDirectory": "../outside"}`)
	put(filepath.Join(filepath.Dir(proj), "outside", "steady-crafting-grove.md"), "# escaped")
	if got := opened(); got != filepath.Join(proj, "docs", "plans", "steady-crafting-grove.md") {
		t.Fatalf("a folder outside the project was followed: %s", got)
	}
}

// Feedback can run over several lines: alt-enter, or a backslash before enter as in Claude Code, starts a
// new one, and a pasted text keeps its own. The footer shows the end of it, where the cursor is.
func TestFeedbackTakesSeveralLines(t *testing.T) {
	a, path := planApp(t)
	if err := os.WriteFile(path, []byte("# plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	press(a, "m", "r", "step 2 is too big", "\x1b\r", "split it\\", "\r", "\x1b[200~and test\r\nfirst\x1b[201~")
	if a.mode != promptMode {
		t.Fatal("enter after a backslash sent the feedback")
	}
	if got := string(a.prompt.text); got != "step 2 is too big\nsplit it\nand test\nfirst" {
		t.Fatalf("text %q", got)
	}
	press(a, strings.Repeat("x", 200))
	lines, cur := a.compose()
	foot := ansi.Strip(lines[len(lines)-1])
	if !cur.show || !strings.Contains(foot, "xxxx") || !strings.Contains(foot, "…") || width(foot) != a.w {
		t.Fatalf("footer %q cursor %+v", foot, cur)
	}
	a.prompt.text = []rune("a\nb")
	lines, _ = a.compose()
	if foot := ansi.Strip(lines[len(lines)-1]); !strings.Contains(foot, "a↵b") {
		t.Fatalf("newline shown as %q", foot)
	}
}

// paneWithPlan is a live pane running a Claude session that has a plan, with the plan open.
func paneWithPlan(t *testing.T, screen ...string) (*App, *fakeServer) {
	t.Helper()
	cfg := t.TempDir()
	path := filepath.Join(cfg, "plans", "p.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, fs := appWithPane()
	a.snap.Sessions = []model.Session{{ID: "s1", Agent: "claude", Name: "api", PID: 4242, SessionsDir: filepath.Join(cfg, "sessions")}}
	a.convs = map[string]chat.Conversation{"s1": {Info: chat.Info{Plan: "p", Mode: "plan"}}}
	fs.screen = &mux.Screen{ID: "p1", Lines: screen}
	a.rebuild()
	a.sel = paneKey("p1")
	feedbackGap = time.Millisecond
	press(a, "m")
	if a.doc == nil {
		t.Fatalf("plan not opened: %q", a.flash.text)
	}
	return a, fs
}

func waitOps(t *testing.T, fs *fakeServer, n int) []mux.Msg {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		var got []mux.Msg
		fs.mu.Lock()
		sent := append([]mux.Msg(nil), fs.sent...)
		fs.mu.Unlock()
		for _, m := range sent {
			if m.Op == mux.OpInput || m.Op == mux.OpPaste {
				got = append(got, m)
			}
		}
		if len(got) >= n {
			return got
		}
	}
	t.Fatalf("fewer than %d keystrokes reached the pane: %+v", n, fs.sent)
	return nil
}

// Claude Code holds a message from another session whose permission mode differs, so feedback for a
// pane is typed into it, as the person would: on the plan's approval menu it picks "Tell Claude what to
// change" and types the feedback there.
func TestFeedbackOnTheApprovalMenuIsTypedIntoIt(t *testing.T) {
	a, fs := paneWithPlan(t,
		" Claude has written up a plan and is ready to execute. Would you like to proceed?",
		"   1. Yes, and use auto mode",
		"   2. Yes, manually approve edits",
		" ❯ 4. Tell Claude what to change",
	)
	press(a, "r", "use greeting.txt", "\x1b\r", "and hello", "\r")
	got := waitOps(t, fs, 3)
	if got[0].Op != mux.OpInput || string(got[0].Data) != "4" ||
		got[1].Op != mux.OpPaste || string(got[1].Data) != "use greeting.txt\nand hello" ||
		got[2].Op != mux.OpInput || string(got[2].Data) != "\r" {
		t.Fatalf("keystrokes %+v", got)
	}
}

func TestFeedbackWithoutTheMenuIsAMessage(t *testing.T) {
	a, fs := paneWithPlan(t, " > ", "   ⏸ plan mode on")
	press(a, "r", "shorter please", "\r")
	got := waitOps(t, fs, 2)
	if got[0].Op != mux.OpPaste || !strings.HasPrefix(string(got[0].Data), "Feedback on your plan (") ||
		!strings.HasSuffix(string(got[0].Data), "shorter please") || string(got[1].Data) != "\r" {
		t.Fatalf("keystrokes %+v", got)
	}
}

func TestHeldNote(t *testing.T) {
	if heldNote("bypass") != "" || !strings.Contains(heldNote("plan"), "approve") || !strings.Contains(heldNote(""), "approve") {
		t.Fatal("a session outside bypass mode may hold the message; one in it does not")
	}
}
