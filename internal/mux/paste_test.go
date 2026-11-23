package mux

import "testing"

func TestPasteModeFollowsProgramOutput(t *testing.T) {
	var m pasteMode
	if got := string(m.wrap([]byte("ls\n"))); got != "ls\n" {
		t.Fatalf("mode off: %q", got)
	}
	// The enabling sequence split across two reads.
	m.observe([]byte("prompt \x1b[?20"))
	m.observe([]byte("04h$ "))
	if !m.on {
		t.Fatal("split DECSET 2004 not seen")
	}
	if got := string(m.wrap([]byte("a\x1b[201~b"))); got != "\x1b[200~ab\x1b[201~" {
		t.Fatalf("mode on: %q", got)
	}
	m.observe([]byte("\x1b[?2004h running \x1b[?2004l"))
	if m.on {
		t.Fatal("later reset ignored")
	}
}
