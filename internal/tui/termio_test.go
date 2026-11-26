package tui

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
	"time"
)

// A frame that changes nothing writes nothing: a terminal redraws for the sync and cursor sequences
// alone, and the dashboard draws whenever a pane prints.
func TestUnchangedFrameWritesNothing(t *testing.T) {
	var out bytes.Buffer
	s := &screen{w: bufio.NewWriter(&out)}
	lines := []string{"header", "card", "footer"}
	s.frame(lines, 10, 3, cursor{})
	if out.Len() == 0 {
		t.Fatal("the first frame wrote nothing")
	}

	out.Reset()
	s.frame([]string{"header", "card", "footer"}, 10, 3, cursor{})
	if out.Len() != 0 {
		t.Fatalf("an unchanged frame wrote %q", out.String())
	}

	out.Reset()
	s.frame([]string{"header", "card 2", "footer"}, 10, 3, cursor{})
	if got := out.String(); !strings.Contains(got, "\x1b[2;1Hcard 2") || strings.Contains(got, "header") || strings.Contains(got, "footer") {
		t.Fatalf("a changed line: %q", got)
	}

	out.Reset()
	s.frame([]string{"header", "card 2", "footer"}, 10, 3, cursor{x: 2, y: 1, show: true})
	if got := out.String(); !strings.Contains(got, "\x1b[2;3H") {
		t.Fatalf("a moved cursor was not drawn: %q", got)
	}
}

// Frames follow the person at full rate and pane output at a calmer one.
func TestFrameRateFollowsThePerson(t *testing.T) {
	a := &App{}
	now := time.Now()
	a.lastDraw = now.Add(-frameInterval)
	if a.frameDue(now) {
		t.Fatal("pane output alone drew at the full rate")
	}
	a.lastDraw = now.Add(-quietFrame)
	if !a.frameDue(now) {
		t.Fatal("pane output was never drawn")
	}
	a.lastInput, a.lastDraw = now.Add(-time.Millisecond), now.Add(-frameInterval)
	if !a.frameDue(now) {
		t.Fatal("a key did not draw at the full rate")
	}
}
