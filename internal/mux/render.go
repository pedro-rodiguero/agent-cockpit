package mux

import (
	"fmt"
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// Theme colors for what the program leaves at "default", plus a warm 16-color ANSI palette so agent
// output sits in the same burnt-gold scheme as the dashboard instead of stock xterm colors.
var (
	DefaultFg = rgb{0xe9, 0xdf, 0xc9}
	DefaultBg = rgb{0x14, 0x11, 0x0c}
	ansi16    = [16]rgb{
		{0x2a, 0x24, 0x1b}, {0xc2, 0x5a, 0x43}, {0x9a, 0xa8, 0x78}, {0xd4, 0xa2, 0x4c},
		{0x7f, 0x98, 0xa8}, {0xb0, 0x84, 0x9c}, {0x86, 0xa8, 0x9c}, {0xcf, 0xc4, 0xab},
		{0x5f, 0x55, 0x44}, {0xdd, 0x76, 0x5c}, {0xb4, 0xc2, 0x8e}, {0xe8, 0xbe, 0x6a},
		{0x9a, 0xb4, 0xc4}, {0xc8, 0x9e, 0xb6}, {0xa2, 0xc4, 0xb8}, {0xf2, 0xea, 0xd8},
	}
)

type rgb struct{ r, g, b uint8 }

func (c rgb) RGBA() (r, g, b, a uint32) {
	return uint32(c.r) * 0x101, uint32(c.g) * 0x101, uint32(c.b) * 0x101, 0xffff
}

func toRGB(c color.Color) (rgb, bool) {
	switch v := c.(type) {
	case nil:
		return rgb{}, false
	case ansi.BasicColor:
		return ansi16[int(v)%16], true
	case ansi.IndexedColor:
		if int(v) < 16 {
			return ansi16[int(v)], true
		}
	}
	r, g, b, _ := c.RGBA()
	return rgb{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}, true
}

// renderLines draws the visible screen as one ANSI string per row, exactly cols cells wide, with
// every default color replaced by the theme so the result composes on the dashboard background.
func renderLines(e *vt.SafeEmulator, cols, rows int) []string {
	out := make([]string, rows)
	var sb strings.Builder
	for y := 0; y < rows; y++ {
		sb.Reset()
		var last string
		for x := 0; x < cols; {
			c := e.CellAt(x, y)
			content, width := " ", 1
			var st uv.Style
			if c != nil {
				st = c.Style
				if c.Content != "" {
					content = c.Content
				}
				if c.Width > 1 {
					width = c.Width
				}
				if c.Width == 0 && c.Content == "" {
					x++
					continue
				}
			}
			if x+width > cols {
				content, width = " ", 1
			}
			if sgr := sgrFor(st); sgr != last {
				sb.WriteString(sgr)
				last = sgr
			}
			sb.WriteString(content)
			x += width
		}
		sb.WriteString("\x1b[0m")
		out[y] = sb.String()
	}
	return out
}

func sgrFor(s uv.Style) string {
	fg, okF := toRGB(s.Fg)
	bg, okB := toRGB(s.Bg)
	if !okF {
		fg = DefaultFg
	}
	if !okB {
		bg = DefaultBg
	}
	if s.Attrs&uv.AttrReverse != 0 {
		fg, bg = bg, fg
	}
	var b strings.Builder
	b.WriteString("\x1b[0")
	if s.Attrs&uv.AttrBold != 0 {
		b.WriteString(";1")
	}
	if s.Attrs&uv.AttrFaint != 0 {
		b.WriteString(";2")
	}
	if s.Attrs&uv.AttrItalic != 0 {
		b.WriteString(";3")
	}
	if s.Underline != 0 {
		b.WriteString(";4")
	}
	if s.Attrs&uv.AttrStrikethrough != 0 {
		b.WriteString(";9")
	}
	fmt.Fprintf(&b, ";38;2;%d;%d;%d;48;2;%d;%d;%dm", fg.r, fg.g, fg.b, bg.r, bg.g, bg.b)
	return b.String()
}
