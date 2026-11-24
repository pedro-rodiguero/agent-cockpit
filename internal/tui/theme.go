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

// The theme's colors. useTheme sets them; every drawing function reads them when it draws, so a
// change of theme shows on the next frame.
var (
	cBg, cPanel, cPanelHi, cLine, cLineAttn        rgb
	cInk, cSoft, cMuted, cFaint, cGold, cGoldHi    rgb
	cWaiting, cAsked, cStuck, cBusy, cIdle, cInbox rgb
)

// themeColor is one color of both themes, with the 256-color entry picked by hand for each: the nearest
// entry by distance turns every warm tone into a neutral grey.
type themeColor struct {
	c              *rgb
	dark, light    rgb
	dark256, lt256 uint8
}

// Dark: burnt gold on a warm near-black. Light: the same inks on warm paper. Gold marks structure and
// selection; only states that need the person (waiting, asked, stuck, inbox) get saturated color,
// everything else stays in warm greys.
var themeColors = []themeColor{
	{&cBg, rgb{0x14, 0x11, 0x0c}, rgb{0xf6, 0xf1, 0xe6}, 233, 255},
	{&cPanel, rgb{0x1e, 0x19, 0x12}, rgb{0xec, 0xe5, 0xd5}, 235, 254},
	{&cPanelHi, rgb{0x2b, 0x24, 0x1a}, rgb{0xdf, 0xd5, 0xbf}, 236, 253}, // the selected row on a panel
	{&cLine, rgb{0x3a, 0x31, 0x22}, rgb{0xcd, 0xc0, 0xa3}, 238, 250},
	{&cLineAttn, rgb{0x6b, 0x43, 0x25}, rgb{0xd8, 0xa0, 0x74}, 94, 180}, // border of a card that needs the person
	{&cInk, rgb{0xe9, 0xdf, 0xc9}, rgb{0x2a, 0x23, 0x18}, 187, 235},
	{&cSoft, rgb{0xbf, 0xb1, 0x96}, rgb{0x4e, 0x45, 0x35}, 144, 238},
	{&cMuted, rgb{0x8c, 0x7f, 0x66}, rgb{0x76, 0x6a, 0x54}, 101, 242},
	{&cFaint, rgb{0x5f, 0x55, 0x44}, rgb{0xa3, 0x96, 0x7d}, 241, 246},
	{&cGold, rgb{0xc0, 0x8a, 0x2e}, rgb{0x96, 0x64, 0x10}, 136, 136},
	{&cGoldHi, rgb{0xdc, 0xb1, 0x5a}, rgb{0x7a, 0x4f, 0x08}, 179, 94},
	{&cWaiting, rgb{0xd9, 0x80, 0x3f}, rgb{0xb5, 0x55, 0x16}, 173, 166},
	{&cAsked, rgb{0x7f, 0xae, 0xb0}, rgb{0x2c, 0x70, 0x74}, 109, 30}, // the one cool color: a question stands out
	{&cStuck, rgb{0xc2, 0x5a, 0x43}, rgb{0xa3, 0x37, 0x22}, 167, 124},
	{&cBusy, rgb{0xc9, 0xa5, 0x4e}, rgb{0x8a, 0x66, 0x0c}, 178, 136},
	{&cIdle, rgb{0x6f, 0x65, 0x53}, rgb{0x9a, 0x8e, 0x76}, 243, 245},
	{&cInbox, rgb{0x9a, 0xa8, 0x78}, rgb{0x56, 0x6a, 0x2c}, 107, 64},
}

// terminalBg leaves the page background to the terminal: cells painted cBg get the terminal's default
// background instead, so its own background, or its transparency, shows through.
var terminalBg bool

// appearance is how the dashboard is drawn, from the settings.
type appearance struct {
	light      bool
	terminalBg bool
}

// useTheme sets the colors of the light or the dark theme.
func useTheme(ap appearance) {
	palette256 = make(map[rgb]uint8, len(themeColors))
	for _, t := range themeColors {
		if ap.light {
			*t.c = t.light
			palette256[t.light] = t.lt256
		} else {
			*t.c = t.dark
			palette256[t.dark] = t.dark256
		}
	}
	terminalBg = ap.terminalBg
	looks = map[model.State]stateLook{
		model.Waiting: {"▲", "waiting", cWaiting},
		model.Asked:   {"?", "your turn", cAsked},
		model.Stuck:   {"✕", "stuck?", cStuck},
		model.Inbox:   {"✉", "inbox", cInbox},
		model.Todo:    {"□", "to do", cSoft},
		model.Busy:    {"●", "working", cBusy},
		model.Idle:    {"○", "idle", cIdle},
	}
}

func init() { useTheme(appearance{}) }

func sgr(fg, bg rgb, bold bool) string {
	b := ""
	if bold {
		b = "1;"
	}
	if terminalBg && bg == cBg {
		return fmt.Sprintf("\x1b[0;%s38;2;%d;%d;%dm", b, fg.r, fg.g, fg.b)
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

var looks map[model.State]stateLook

func look(s model.State) stateLook {
	if l, ok := looks[s]; ok {
		return l
	}
	return stateLook{"·", string(s), cMuted}
}
