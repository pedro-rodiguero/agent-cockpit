package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func mdPlain(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(ansi.Strip(l), " ")
	}
	return out
}

func TestRenderMarkdownLayout(t *testing.T) {
	src := strings.Join([]string{
		"# Plan: rate limits",
		"",
		"Add a **token bucket** to `api/limit.go`, see [the doc](https://example.com).",
		"",
		"## Steps",
		"",
		"1. Write the failing test",
		"2. Implement it",
		"   in two parts",
		"- [ ] review",
		"- [x] merged",
		"",
		"| File | Change |",
		"| --- | --- |",
		"| `limit.go` | new |",
		"",
		"```go",
		"func  limit()  {}",
		"```",
		"> careful with snake_case_names",
		"---",
	}, "\n")
	lines, heads := renderMarkdown(src, 60)
	got := mdPlain(lines)
	for _, want := range []string{
		"Plan: rate limits",
		"Add a token bucket to api/limit.go, see the doc.",
		"1. Write the failing test",
		"2. Implement it in two parts",
		"☐ review",
		"☑ merged",
		"File     │ Change",
		"limit.go │ new",
		" func  limit()  {}",
		"│ careful with snake_case_names",
	} {
		found := false
		for _, g := range got {
			if strings.Contains(g, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %q in:\n%s", want, strings.Join(got, "\n"))
		}
	}
	if !strings.Contains(strings.Join(got, "\n"), "api/limit.go") {
		t.Errorf("inline code lost: %q", got)
	}
	if len(heads) != 2 || got[heads[0]] != "Plan: rate limits" || got[heads[1]] != "Steps" {
		t.Fatalf("headings at %v: %q", heads, got)
	}
	for i, l := range lines {
		if w := width(l); w != 60 {
			t.Fatalf("line %d is %d cells: %q", i, w, got[i])
		}
	}
}

func TestRenderMarkdownWraps(t *testing.T) {
	lines, _ := renderMarkdown("- "+strings.Repeat("word ", 30)+"https://example.com/"+strings.Repeat("x", 80), 30)
	got := mdPlain(lines)
	if len(got) < 5 || !strings.HasPrefix(got[0], "• word") || !strings.HasPrefix(got[1], "  word") {
		t.Fatalf("list item not wrapped under its text: %q", got)
	}
	for i, l := range lines {
		if width(l) != 30 {
			t.Fatalf("line %d is %d cells", i, width(l))
		}
	}
}

// The document is agent output: no escape sequence or control character in it may reach the terminal.
func TestRenderMarkdownStripsControlCharacters(t *testing.T) {
	evil := "# Title\x1b]0;pwned\x07\n\nText \x1b[2J\x1b]52;c;cm0gLXJmIH4=\x07 and \x9b31m C1 \x1b[201~\nrm -rf x\r\n```\ncode\x1b[31m\x00\n```"
	lines, _ := renderMarkdown(evil, 50)
	for i, l := range lines {
		// Every escape left must be one the renderer wrote: SGR only.
		rest := l
		for {
			j := strings.IndexByte(rest, 0x1b)
			if j < 0 {
				break
			}
			end := strings.IndexByte(rest[j:], 'm')
			if end < 0 || !strings.HasPrefix(rest[j:], "\x1b[") || strings.ContainsAny(rest[j+2:j+end], "]\x07") {
				t.Fatalf("line %d carries a foreign escape: %q", i, l)
			}
			rest = rest[j+end+1:]
		}
		for _, c := range ansi.Strip(l) {
			if c < 0x20 || c == 0x7f || (c >= 0x80 && c <= 0x9f) {
				t.Fatalf("line %d carries control character %U: %q", i, c, l)
			}
		}
	}
}
