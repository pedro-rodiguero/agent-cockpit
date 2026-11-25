package mux

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

func screenRow(e *vt.SafeEmulator, w int) string {
	var b strings.Builder
	for x := 0; x < w; x++ {
		if c := e.CellAt(x, 0); c != nil && c.Content != "" {
			b.WriteString(c.Content)
		} else {
			b.WriteString(" ")
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// The spinner glyphs Claude Code puts in its title (✳ ✻ ✶ ✢ ✽) hold the byte 0x9C, which the emulator
// took for the end of the title, printing the rest of it at the cursor.
func TestTitleWithC1ByteStaysOffScreen(t *testing.T) {
	out := []byte("push it\x1b]0;✳ Navegação por setas no cockpit\x07 now")
	for cut := 0; cut <= len(out); cut++ {
		e := vt.NewSafeEmulator(50, 1)
		go io.Copy(io.Discard, e)
		var title string
		f := oscFilter{title: func(s string) { title = s }}
		e.Write(f.feed(out[:cut]))
		e.Write(f.feed(out[cut:]))
		if got := screenRow(e, 50); got != "push it now" || title != "✳ Navegação por setas no cockpit" {
			t.Fatalf("cut %d: screen %q title %q", cut, got, title)
		}
	}
}

func TestOSCFilterPassesTheRestThrough(t *testing.T) {
	var f oscFilter
	for _, s := range []string{
		"plain text",
		"\x1b[1;31mred\x1b[0m",
		"\x1b]0;ascii title\x07",
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
		"ç ✳ outside any OSC",
	} {
		if got := string(f.feed([]byte(s))); got != s {
			t.Fatalf("%q became %q", s, got)
		}
	}
	if got := string(f.feed([]byte("a\x1b]8;;https://exã.com\x07b"))); got != "ab" {
		t.Fatalf("non-ASCII hyperlink: %q", got)
	}
	if got := string(f.feed([]byte("x\x1b"))); got != "x" || string(f.feed([]byte("[A"))) != "\x1b[A" {
		t.Fatal("an ESC at the end of a read was not held for the next one")
	}
}
