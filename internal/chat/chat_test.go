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
`
	if got := render(parseClaude([]byte(log)).Lines); got != want {
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
	if got := render(parseCodex([]byte(log)).Lines); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseClaudeInfo(t *testing.T) {
	assistant := func(content string) string {
		return `{"type":"assistant","gitBranch":"feat/limits","effort":"high","message":{"role":"assistant","model":"claude-opus-5-5",` +
			`"usage":{"input_tokens":10,"cache_creation_input_tokens":200,"cache_read_input_tokens":3000},"content":[` + content + `]}}`
	}
	log := []string{
		`{"type":"permission-mode","permissionMode":"plan"}`,
		`{"type":"user","slug":"steady-crafting-grove","message":{"role":"user","content":"run the tests"}}`,
		assistant(`{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"npm test"}},{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"/srv/a.ts"}}`),
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"ok"}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"role":"assistant","model":"claude-haiku-5-5","usage":{"input_tokens":1},"content":[]}}`,
	}
	info := parseClaude([]byte(strings.Join(log, "\n"))).Info
	want := Info{Model: "opus-5-5", Branch: "feat/limits", Effort: "high", Mode: "plan", Plan: "steady-crafting-grove", ContextTokens: 3210, Activity: "Bash npm test", Running: 1}
	if info != want {
		t.Fatalf("mid-turn:\n got %+v\nwant %+v", info, want)
	}

	// The remaining call returns: nothing is running any more.
	log = append(log, `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`)
	if info := parseClaude([]byte(strings.Join(log, "\n"))).Info; info.Activity != "" || info.Running != 0 {
		t.Fatalf("after results: %+v", info)
	}

	// A call whose result never arrives is dropped when the person interrupts.
	log = append(log, assistant(`{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"sleep 99"}}`),
		`{"type":"user","message":{"role":"user","content":"[Request interrupted by user]"}}`)
	if info := parseClaude([]byte(strings.Join(log, "\n"))).Info; info.Running != 0 {
		t.Fatalf("after interrupt: %+v", info)
	}
}

func TestParseCodexInfo(t *testing.T) {
	log := strings.Join([]string{
		`{"type":"turn_context","payload":{"model":"gpt-6.1","cwd":"/src"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":36831},"model_context_window":475000}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":null}}`,
	}, "\n")
	want := Info{Model: "gpt-6.1", ContextTokens: 36831, ContextWindow: 475000}
	if info := parseCodex([]byte(log)).Info; info != want {
		t.Fatalf("got %+v, want %+v", info, want)
	}
}

func TestReaderCachesUntilTheFileChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	line := `{"type":"user","message":{"role":"user","content":"%s"}}` + "\n"
	if err := os.WriteFile(path, []byte(fmt.Sprintf(line, "one")), 0o600); err != nil {
		t.Fatal(err)
	}
	var r Reader
	if got := render(r.Read("claude", path, 10).Lines); got != "user: one\n" {
		t.Fatalf("first read: %q", got)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, line, "two")
	f.Close()
	if got := render(r.Read("claude", path, 1).Lines); got != "user: two\n" {
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
	for _, l := range r.Read(agent, path, 14).Lines {
		t.Logf("%d %.110s", l.Kind, l.Text)
	}
}

func TestQuestion(t *testing.T) {
	conv := func(lines ...Line) Conversation { return Conversation{Lines: lines} }
	for _, tc := range []struct {
		conv Conversation
		want string
	}{
		{conv(), ""},
		{conv(Line{User, "fix it"}, Line{Assistant, "Fixed. Should I also update the docs?"}), "Fixed. Should I also update the docs?"},
		{conv(Line{Assistant, "Should I start with the first four?**"}), "Should I start with the first four?**"},
		{conv(Line{Assistant, "Want me to push? (it is not merged)"}), ""},
		{conv(Line{Assistant, "Is it the cache?"}, Line{Assistant, "It was the cache."}), ""},
		{conv(Line{Assistant, "Should I run it?"}, Line{Tool, "Bash npm test"}), ""},
		{conv(Line{Assistant, "Should I run it?"}, Line{User, "yes"}), ""},
	} {
		if got := tc.conv.Question(); got != tc.want {
			t.Errorf("Question(%v) = %q, want %q", tc.conv.Lines, got, tc.want)
		}
	}
}

func TestProse(t *testing.T) {
	for in, want := range map[string]string{
		"  plain **bold** text ":   "plain bold text",
		"## Lista do dia":          "Lista do dia",
		"#hashtag":                 "#hashtag",
		"|---|:---:|---|":          "",
		"---":                      "",
		"| 15/11 | 166 |  | 166 |": "15/11 · 166 · 166",
		"a | b":                    "a | b",
	} {
		if got := prose(in); got != want {
			t.Errorf("prose(%q) = %q, want %q", in, got, want)
		}
	}
}
