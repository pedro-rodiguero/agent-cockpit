package mux

import "testing"

func TestMouseModeFollowsProgramOutput(t *testing.T) {
	var m mouseMode
	if got := m.wheel(true, 4, 2, false); got != nil {
		t.Fatalf("plain screen: %q", got)
	}
	if got := string(m.wheel(true, 4, 2, true)); got != "\x1b[A\x1b[A\x1b[A" {
		t.Fatalf("full-screen without mouse: %q", got)
	}
	// Tracking and SGR together, split across two reads.
	m.observe([]byte("draw \x1b[?1002;10"))
	m.observe([]byte("06h more"))
	if got := string(m.wheel(true, 4, 2, true)); got != "\x1b[<64;5;3M" {
		t.Fatalf("sgr up: %q", got)
	}
	if got := string(m.wheel(false, 0, 0, true)); got != "\x1b[<65;1;1M" {
		t.Fatalf("sgr down: %q", got)
	}
	m.observe([]byte("\x1b[?1006l"))
	if got := string(m.wheel(true, 4, 2, true)); got != "\x1b[M`%#" {
		t.Fatalf("x10: %q", got)
	}
	m.observe([]byte("\x1b[?1002l\x1b[?25h"))
	if got := m.wheel(false, 0, 0, false); got != nil {
		t.Fatalf("after reset: %q", got)
	}
}
