package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/chat"
	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
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
	a, _ := planApp(t)
	press(a, "m")
	if a.doc != nil || !strings.Contains(a.flash.text, "plan not found") {
		t.Fatalf("missing file: doc %v flash %q", a.doc, a.flash.text)
	}
	for _, evil := range []string{"../../.ssh/notes", "a/b", "..", "x\x1b[2J"} {
		a.convs["s1"] = chat.Conversation{Info: chat.Info{Plan: evil}}
		press(a, "m")
		if a.doc != nil || !strings.Contains(a.flash.text, "not a plan name") {
			t.Fatalf("slug %q: doc %v flash %q", evil, a.doc, a.flash.text)
		}
	}
	a.convs["s1"] = chat.Conversation{}
	press(a, "m")
	if a.doc != nil || !strings.Contains(a.flash.text, "not been in plan mode") {
		t.Fatalf("no slug: doc %v flash %q", a.doc, a.flash.text)
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
