package tui

import (
	"errors"
	"reflect"
	"testing"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

func TestResumeSpec(t *testing.T) {
	t.Setenv("COCKPIT_CLAUDE_DIRS", "/cfg/.claude:/cfg/.claude-work")
	cases := []struct {
		name    string
		s       model.Session
		args    []string
		env     []string
		wantErr bool
	}{
		{"claude default profile", model.Session{Agent: "claude", NativeID: "abc", PID: 1, Cwd: "/x", SessionsDir: "/cfg/.claude/sessions"},
			[]string{"--resume", "abc"}, nil, false},
		{"claude other profile", model.Session{Agent: "claude", NativeID: "abc", PID: 1, Cwd: "/x", SessionsDir: "/cfg/.claude-work/sessions"},
			[]string{"--resume", "abc"}, []string{"CLAUDE_CONFIG_DIR=/cfg/.claude-work"}, false},
		{"codex", model.Session{Agent: "codex", NativeID: "r1", PID: 1, Cwd: "/x"}, []string{"resume", "r1"}, nil, false},
		{"agy", model.Session{Agent: "agy", NativeID: "z", PID: 1, Cwd: "/x"}, nil, nil, true},
		{"no id", model.Session{Agent: "codex", PID: 1, Cwd: "/x"}, nil, nil, true},
		{"no pid", model.Session{Agent: "codex", NativeID: "r1", Cwd: "/x"}, nil, nil, true},
	}
	for _, c := range cases {
		m, err := resumeSpec(c.s)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v", c.name, err)
			continue
		}
		if c.wantErr {
			continue
		}
		if !reflect.DeepEqual(m.Args, c.args) || !reflect.DeepEqual(m.Env, c.env) || m.Cwd != c.s.Cwd || m.Agent != c.s.Agent {
			t.Errorf("%s: spec = %+v", c.name, m)
		}
	}
}

func TestResumeCommand(t *testing.T) {
	t.Setenv("COCKPIT_CLAUDE_DIRS", "/cfg/.claude:/cfg/.claude-work")
	s := model.Session{Agent: "claude", NativeID: "abc-1", Cwd: "/my dir/it's", SessionsDir: "/cfg/.claude-work/sessions"}
	want := `cd '/my dir/it'\''s' && CLAUDE_CONFIG_DIR=/cfg/.claude-work claude --resume abc-1`
	if got := resumeCommand(s); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestAdoptTarget(t *testing.T) {
	sessions := []model.Session{{ID: "a", PID: 10}, {ID: "b", PID: 20}}
	if s, err := adoptTarget(sessions, "b", 20); err != nil || s.ID != "b" {
		t.Errorf("same session: %+v, %v", s, err)
	}
	if _, err := adoptTarget(sessions, "b", 21); !errors.Is(err, errSessionChanged) {
		t.Errorf("new pid accepted: %v", err)
	}
	if _, err := adoptTarget(sessions, "gone", 10); !errors.Is(err, errSessionChanged) {
		t.Errorf("vanished session accepted: %v", err)
	}
}

func TestStopAgentChecksTheProcess(t *testing.T) {
	s := model.Session{Agent: "claude", PID: 77}
	var stopped []int
	stop := func(pid int) error { stopped = append(stopped, pid); return nil }

	notAgent := func(int, string) (bool, error) { return false, nil }
	if err := stopAgent(s, notAgent, stop); err == nil || len(stopped) != 0 {
		t.Fatalf("stopped a reused pid: err %v, stopped %v", err, stopped)
	}
	broken := func(int, string) (bool, error) { return false, errors.New("ps failed") }
	if err := stopAgent(s, broken, stop); err == nil || len(stopped) != 0 {
		t.Fatalf("stopped without a check: err %v, stopped %v", err, stopped)
	}
	var asked []string
	isAgent := func(pid int, agent string) (bool, error) { asked = append(asked, agent); return pid == 77, nil }
	if err := stopAgent(s, isAgent, stop); err != nil || !reflect.DeepEqual(stopped, []int{77}) || asked[0] != "claude" {
		t.Fatalf("err %v, stopped %v, asked %v", err, stopped, asked)
	}
}

func TestCommandIsAgent(t *testing.T) {
	cases := []struct {
		args, agent string
		want        bool
	}{
		{"claude --resume x", "claude", true},
		{"/Users/me/.local/bin/claude", "claude", true},
		{"node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js", "claude", true},
		{"/opt/bin/codex resume r1", "codex", true},
		{"node /Users/me/.claude/hooks/notify.js", "claude", false},
		{"vim notes-about-claude.md", "claude", false},
		{"", "claude", false},
	}
	for _, c := range cases {
		if got := commandIsAgent(c.args, c.agent); got != c.want {
			t.Errorf("commandIsAgent(%q, %q) = %v", c.args, c.agent, got)
		}
	}
}

func TestStopProcessRefusesGroupPIDs(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if err := stopProcess(pid); err == nil {
			t.Errorf("stopProcess(%d) did not refuse", pid)
		}
	}
}
