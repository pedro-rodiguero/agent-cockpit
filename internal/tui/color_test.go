package tui

import "testing"

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDetectColorDepth(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want colorDepth
	}{
		{map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, color256},
		{map[string]string{}, color256},
		{map[string]string{"COLORTERM": "truecolor"}, trueColor},
		{map[string]string{"TERM_PROGRAM": "iTerm.app"}, trueColor},
		{map[string]string{"COLORTERM": "truecolor", "COCKPIT_COLOR": "256"}, color256},
		{map[string]string{"TERM_PROGRAM": "Apple_Terminal", "COCKPIT_COLOR": "truecolor"}, trueColor},
	}
	for _, c := range cases {
		if got := detectColorDepth(env(c.env)); got != c.want {
			t.Errorf("%v: got %v, want %v", c.env, got, c.want)
		}
	}
}

func TestNearest256(t *testing.T) {
	cases := map[rgb]uint8{
		{0, 0, 0}:       16,
		{255, 255, 255}: 231,
		{255, 0, 0}:     196,
		{18, 18, 18}:    233,
		{128, 128, 128}: 244,
		{215, 175, 95}:  179,
	}
	for c, want := range cases {
		if got := nearest256(c); got != want {
			t.Errorf("nearest256(%v) = %d, want %d", c, got, want)
		}
	}
}

func TestDownsampleLine(t *testing.T) {
	d := newDownsampler()
	in := sgr(cGold, cBg, true) + "hi" + "\x1b[0;3;38;2;255;0;0;48;2;0;0;0mred" + "\x1b[5;1H" + "\x1b[0m" + "\x1b[?25l"
	want := "\x1b[0;1;38;5;136;48;5;233mhi" + "\x1b[0;3;38;5;196;48;5;16mred" + "\x1b[5;1H" + "\x1b[0m" + "\x1b[?25l"
	if got := d.line(in); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if got := d.line("plain"); got != "plain" {
		t.Errorf("plain text changed: %q", got)
	}
}
