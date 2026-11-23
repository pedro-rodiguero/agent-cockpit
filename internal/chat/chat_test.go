package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func render(lines []Line) string {
	kind := map[Kind]string{User: "user", Assistant: "agent", Tool: "tool", Note: "note"}
	var b strings.Builder
	for _, l := range lines {
		fmt.Fprintf(&b, "%s: %s\n", kind[l.Kind], l.Text)
	}
	return b.String()
}

func TestParseClaude(t *testing.T) {
	log := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"add rate   limiting\nto login"}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"Caveat: internal"}}`,
		`{"type":"user","message":{"role":"user","content":"<command-name>/clear</command-name>"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[` +
			`{"type":"text","text":"I'll add a **limiter**.\n\nFirst the store."},` +
			`{"type":"tool_use","name":"Edit","input":{"file_path":"/srv/app/limits.ts","old_string":"x"}},` +
			`{"type":"tool_use","name":"mcp__github__create_pr","input":{"title":"Rate limit"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
		`{"type":"user","message":{"role":"user","content":"[Request interrupted by user]"}}`,
		`{"type":"user","origin":{"kind":"peer","name":"reviewer"},"message":{"role":"user","content":"<cross-session-message from-name=\"reviewer\">\nlooks good\n</cross-session-message>"}}`,
		`{"type":"user","isCompactSummary":true,"message":{"role":"user","content":"This session is being continued..."}}`,
		`{"type":"system","content":"ignored"}`,
		`not json`,
	}, "\n")
	want := `user: add rate limiting to login
agent: I'll add a limiter.
agent: First the store.
tool: Edit /srv/app/limits.ts
tool: github.create_pr Rate limit
note: interrupted
user: [reviewer] looks good
note: conversation compacted
`
	if got := render(parseClaude([]byte(log))); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseCodex(t *testing.T) {
	ev := func(item string) string {
		return `{"type":"event_msg","payload":{"type":"item_completed","item":` + item + `}}`
	}
	log := strings.Join([]string{
		ev(`{"type":"UserMessage","content":[{"text":"fix the build"}]}`),
		ev(`{"type":"CommandExecution","command":["/bin/zsh","-lc","npm run build"]}`),
		ev(`{"type":"FileChange","changes":{"/srv/b.ts":{},"/srv/a.ts":{}}}`),
		ev(`{"type":"McpToolCall","server":"github","tool":"get_issue"}`),
		ev(`{"type":"ImageView","path":"/tmp/shot.png"}`),
		ev(`{"type":"AgentMessage","content":[{"text":"Fixed.\n\nThe **import** was wrong."}]}`),
		`{"type":"event_msg","payload":{"type":"turn_aborted"}}`,
		`{"type":"event_msg","payload":{"type":"token_count"}}`,
	}, "\n")
	want := `user: fix the build
tool: Bash npm run build
tool: Edit /srv/a.ts, /srv/b.ts
tool: github.get_issue
tool: View shot.png
agent: Fixed.
agent: The import was wrong.
note: interrupted
`
	if got := render(parseCodex([]byte(log))); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestReaderCachesUntilTheFileChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	line := `{"type":"user","message":{"role":"user","content":"%s"}}` + "\n"
	if err := os.WriteFile(path, []byte(fmt.Sprintf(line, "one")), 0o600); err != nil {
		t.Fatal(err)
	}
	var r Reader
	if got := render(r.Read("claude", path, 10)); got != "user: one\n" {
		t.Fatalf("first read: %q", got)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, line, "two")
	f.Close()
	if got := render(r.Read("claude", path, 1)); got != "user: two\n" {
		t.Fatalf("after append, last line: %q", got)
	}
}

func TestTilde(t *testing.T) {
	home, _ := os.UserHomeDir()
	for in, want := range map[string]string{
		home:                "~",
		home + "/src":       "~/src",
		home + "x/src":      home + "x/src", // a sibling that shares the prefix
		"/elsewhere" + home: "/elsewhere" + home,
	} {
		if got := tilde(in); got != want {
			t.Errorf("tilde(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestProbe prints what the parser makes of a real log:
//
//	COCKPIT_CHAT=claude:/path/to/session.jsonl go test ./internal/chat -run TestProbe -v
func TestProbe(t *testing.T) {
	agent, path, ok := strings.Cut(os.Getenv("COCKPIT_CHAT"), ":")
	if !ok {
		t.Skip("COCKPIT_CHAT not set")
	}
	var r Reader
	for _, l := range r.Read(agent, path, 14) {
		t.Logf("%d %.110s", l.Kind, l.Text)
	}
}
