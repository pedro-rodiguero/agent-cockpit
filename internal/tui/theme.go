package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

// rgb is a 24-bit terminal color.
type rgb struct{ r, g, b uint8 }

// Burnt-gold palette on a warm near-black. Gold marks structure and selection; only states that
// need the person (waiting, stuck, inbox) get saturated color, everything else stays in warm greys.
var (
	cBg       = rgb{0x14, 0x11, 0x0c}
	cPanel    = rgb{0x1e, 0x19, 0x12}
	cLine     = rgb{0x3a, 0x31, 0x22}
	cLineAttn = rgb{0x6b, 0x43, 0x25} // card border of a session that needs the person
	cInk      = rgb{0xe9, 0xdf, 0xc9}
	cSoft     = rgb{0xbf, 0xb1, 0x96}
	cMuted    = rgb{0x8c, 0x7f, 0x66}
	cFaint    = rgb{0x5f, 0x55, 0x44}
	cGold     = rgb{0xc0, 0x8a, 0x2e}
	cGoldHi   = rgb{0xdc, 0xb1, 0x5a}
	cWaiting  = rgb{0xd9, 0x80, 0x3f}
	cAsked    = rgb{0x7f, 0xae, 0xb0} // the one cool color: a question for the person stands out from the warm greys
	cStuck    = rgb{0xc2, 0x5a, 0x43}
	cBusy     = rgb{0xc9, 0xa5, 0x4e}
	cIdle     = rgb{0x6f, 0x65, 0x53}
	cInbox    = rgb{0x9a, 0xa8, 0x78}
)

func sgr(fg, bg rgb, bold bool) string {
	b := ""
	if bold {
		b = "1;"
	}
	return fmt.Sprintf("\x1b[0;%s38;2;%d;%d;%d;48;2;%d;%d;%dm", b, fg.r, fg.g, fg.b, bg.r, bg.g, bg.b)
}

// paint styles s; every segment carries its full style so segments compose in any order.
func paint(fg, bg rgb, s string) string { return sgr(fg, bg, false) + s }

// paintBold is paint in bold.
func paintBold(fg, bg rgb, s string) string { return sgr(fg, bg, true) + s }

// width is the number of cells s occupies, ignoring escape sequences. Every display width in the
// dashboard goes through it (or through ansi.Truncate), so measuring and cutting always agree.
func width(s string) int { return ansi.StringWidth(s) }

// pad fills a styled line with bg up to w cells, or cuts it at w.
func pad(s string, w int, bg rgb) string {
	n := width(s)
	if n > w {
		return ansi.Truncate(s, w, "")
	}
	return s + paint(cInk, bg, strings.Repeat(" ", w-n))
}

// fit cuts or pads plain text to exactly w cells, collapsing whitespace and marking a cut with "…".
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	if width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", max(0, w-width(s)))
}

// fitLeft is fit for paths: it keeps the end of s, where the distinguishing part is.
func fitLeft(s string, w int) string {
	if width(s) <= w {
		return fit(s, w)
	}
	return fit("…"+ansi.TruncateLeft(s, width(s)-(w-1), ""), w)
}

// wrap breaks plain text into lines of at most w cells. A single character wider than w (a wide
// character when w is 1) gets a line of its own rather than being dropped.
func wrap(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			for width(word) > w {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				cut := ansi.Truncate(word, w, "")
				if cut == "" {
					_, n := utf8.DecodeRuneInString(word)
					cut = word[:n]
				}
				out = append(out, cut)
				word = word[len(cut):]
			}
			switch {
			case word == "":
			case line == "":
				line = word
			case width(line)+1+width(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

type stateLook struct {
	glyph string
	label string
	color rgb
}

var looks = map[model.State]stateLook{
	model.Waiting: {"▲", "waiting", cWaiting},
	model.Asked:   {"?", "your turn", cAsked},
	model.Stuck:   {"✕", "stuck?", cStuck},
	model.Inbox:   {"✉", "inbox", cInbox},
	model.Busy:    {"●", "working", cBusy},
	model.Idle:    {"○", "idle", cIdle},
}

func look(s model.State) stateLook {
	if l, ok := looks[s]; ok {
		return l
	}
	return stateLook{"·", string(s), cMuted}
}
