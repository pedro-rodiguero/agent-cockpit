package tui

import (
	"strconv"
	"strings"
)

// colorDepth is how many colors the terminal can show.
type colorDepth int

const (
	trueColor colorDepth = iota // 24-bit SGR (38;2;r;g;b)
	color256                    // the xterm 256-color palette (38;5;n)
)

// detectColorDepth decides how to emit colors. COCKPIT_COLOR (truecolor or 256) overrides it; otherwise
// 24-bit color is used only where the terminal announces it. macOS Terminal.app before macOS 26 does
// not, and mis-renders 24-bit sequences as unrelated colors instead of ignoring them.
func detectColorDepth(getenv func(string) string) colorDepth {
	switch strings.ToLower(getenv("COCKPIT_COLOR")) {
	case "truecolor", "24bit":
		return trueColor
	case "256":
		return color256
	}
	switch strings.ToLower(getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return trueColor
	}
	switch getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "ghostty", "vscode", "kitty", "Hyper", "agent-cockpit":
		return trueColor
	}
	return color256
}

// palette256 maps the theme's colors to the 256-color entries useTheme picked for them.
var palette256 map[rgb]uint8

// downsampler rewrites 24-bit color sequences as 256-color ones. It is used by one goroutine.
type downsampler struct {
	cache map[rgb]uint8
}

func newDownsampler() *downsampler {
	d := &downsampler{cache: make(map[rgb]uint8, len(palette256))}
	for c, i := range palette256 {
		d.cache[c] = i
	}
	return d
}

func (d *downsampler) index(c rgb) uint8 {
	if i, ok := d.cache[c]; ok {
		return i
	}
	i := nearest256(c)
	d.cache[c] = i
	return i
}

// line rewrites every "38;2;r;g;b" and "48;2;r;g;b" inside the SGR sequences of s.
func (d *downsampler) line(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for {
		start := strings.Index(s, "\x1b[")
		if start < 0 {
			break
		}
		end := start + 2
		for end < len(s) && (s[end] == ';' || (s[end] >= '0' && s[end] <= '9')) {
			end++
		}
		if end >= len(s) || s[end] != 'm' {
			// Not an SGR sequence: copy it through untouched.
			b.WriteString(s[:end])
			s = s[end:]
			continue
		}
		b.WriteString(s[:start+2])
		b.WriteString(d.params(s[start+2 : end]))
		b.WriteByte('m')
		s = s[end+1:]
	}
	b.WriteString(s)
	return b.String()
}

func (d *downsampler) params(p string) string {
	if !strings.Contains(p, "8;2;") {
		return p
	}
	f := strings.Split(p, ";")
	out := make([]string, 0, len(f))
	for i := 0; i < len(f); i++ {
		if (f[i] == "38" || f[i] == "48") && i+4 < len(f) && f[i+1] == "2" {
			r, _ := strconv.Atoi(f[i+2])
			g, _ := strconv.Atoi(f[i+3])
			bl, _ := strconv.Atoi(f[i+4])
			out = append(out, f[i], "5", strconv.Itoa(int(d.index(rgb{uint8(r), uint8(g), uint8(bl)}))))
			i += 4
			continue
		}
		out = append(out, f[i])
	}
	return strings.Join(out, ";")
}

// cubeLevels are the six intensities of each channel in the 6×6×6 color cube (entries 16–231).
var cubeLevels = [6]int{0, 95, 135, 175, 215, 255}

// nearest256 is the closest entry of the color cube or the grey ramp (232–255). The first 16 entries
// are skipped because terminals let users redefine them.
func nearest256(c rgb) uint8 {
	level := func(v uint8) (idx, val int) {
		best := 0
		for i, l := range cubeLevels {
			if abs(int(v)-l) < abs(int(v)-cubeLevels[best]) {
				best = i
			}
		}
		return best, cubeLevels[best]
	}
	ri, rv := level(c.r)
	gi, gv := level(c.g)
	bi, bv := level(c.b)
	cube, cubeDist := 16+36*ri+6*gi+bi, dist(c, rv, gv, bv)

	// Grey ramp: 232 is level 8, each step adds 10.
	avg := (int(c.r) + int(c.g) + int(c.b)) / 3
	step := min(23, max(0, (avg-8+5)/10))
	grey := 8 + 10*step
	if dist(c, grey, grey, grey) < cubeDist {
		return uint8(232 + step)
	}
	return uint8(cube)
}

func dist(c rgb, r, g, b int) int {
	dr, dg, db := int(c.r)-r, int(c.g)-g, int(c.b)-b
	return dr*dr + dg*dg + db*db
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
