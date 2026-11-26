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

// A paste must reach the program as one paste: text that rebuilds an end marker once another is taken
// out of it, or that carries other escape sequences, must not end it early and run what follows.
func TestPasteCannotEndItselfEarly(t *testing.T) {
	m := pasteMode{on: true}
	for _, in := range []string{
		"a\x1b[20\x1b[201~1~\nrm -rf x\n",
		"a\x1b[2\x1b[20\x1b[201~1~1~\nrm -rf x\n",
		"a\x1b\x1b[201~[201~\nrm -rf x\n",
		"a\x1b[201~\nrm -rf x\n",
	} {
		got := m.wrap([]byte(in))
		inner := got[len(pasteStart) : len(got)-len(pasteEnd)]
		if string(got[:len(pasteStart)]) != string(pasteStart) || string(got[len(got)-len(pasteEnd):]) != string(pasteEnd) {
			t.Fatalf("%q: not wrapped: %q", in, got)
		}
		for _, b := range inner {
			if b == escByte {
				t.Fatalf("%q: escape left inside the paste: %q", in, got)
			}
		}
	}
	off := pasteMode{}
	if got := string(off.wrap([]byte("a\x1b[Ab\n"))); got != "a[Ab\n" {
		t.Fatalf("mode off: %q", got)
	}
}
