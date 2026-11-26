package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// renderMarkdown lays out a Markdown document as styled lines of at most w cells, on cBg. It also
// returns the index of the line each heading starts on, for jumping between sections.
//
// The text comes from an agent, so it is untrusted: every control character, escape sequence included,
// is taken out before anything is laid out, and none can reach the terminal.
func renderMarkdown(src string, w int) (lines []string, headings []int) {
	if w < 8 {
		w = 8
	}
	r := &mdRenderer{w: w}
	r.render(strings.Split(cleanText(src), "\n"))
	return r.out, r.heads
}

// cleanText removes C0 and C1 control characters and DEL, except newlines; a tab becomes spaces.
func cleanText(s string) string {
	return strings.Map(func(c rune) rune {
		switch {
		case c == '\n':
			return c
		case c == '\t':
			return ' '
		case c < 0x20, c == 0x7f, c >= 0x80 && c <= 0x9f:
			return -1
		}
		return c
	}, strings.ReplaceAll(s, "\r\n", "\n"))
}

type mdRenderer struct {
	w     int
	out   []string
	heads []int
}

func (r *mdRenderer) emit(s string) { r.out = append(r.out, pad(s, r.w, cBg)) }

// gap adds an empty line unless the document is empty or already ends with one.
func (r *mdRenderer) gap() {
	if n := len(r.out); n > 0 && ansi.Strip(r.out[n-1]) != strings.Repeat(" ", r.w) {
		r.emit("")
	}
}

func (r *mdRenderer) render(src []string) {
	for i := 0; i < len(src); {
		line := src[i]
		trim := strings.TrimSpace(line)
		switch {
		case trim == "":
			i++
		case strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~"):
			i = r.code(src, i)
		case headingLevel(trim) > 0:
			r.heading(trim)
			i++
		case isRule(trim):
			r.gap()
			r.emit(paint(cLine, cBg, strings.Repeat("─", r.w)))
			r.gap()
			i++
		case strings.HasPrefix(trim, "|"):
			i = r.table(src, i)
		case strings.HasPrefix(trim, ">"):
			i = r.quote(src, i)
		case listMarker(line) != "":
			i = r.list(src, i)
		default:
			i = r.paragraph(src, i)
		}
	}
	for len(r.out) > 0 && ansi.Strip(r.out[len(r.out)-1]) == strings.Repeat(" ", r.w) {
		r.out = r.out[:len(r.out)-1]
	}
}

func headingLevel(s string) int {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n == len(s) || s[n] != ' ' {
		return 0
	}
	return n
}

func isRule(s string) bool {
	s = strings.ReplaceAll(s, " ", "")
	if len(s) < 3 {
		return false
	}
	return strings.Trim(s, "-") == "" || strings.Trim(s, "*") == "" || strings.Trim(s, "_") == ""
}

func (r *mdRenderer) heading(s string) {
	level := headingLevel(s)
	text := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s[level:]), "#"))
	r.gap()
	r.heads = append(r.heads, len(r.out))
	color := cInk
	switch level {
	case 1:
		color = cGoldHi
	case 2:
		color = cGold
	}
	for _, l := range r.wrapSpans(inlineSpans(text, true), "", "", color) {
		r.emit(l)
	}
	if level <= 2 {
		r.emit(paint(cLine, cBg, strings.Repeat("─", min(r.w, max(width(text), 3)))))
	}
	r.emit("")
}

// code lays out a fenced block as it is, cut at the right edge rather than wrapped.
func (r *mdRenderer) code(src []string, i int) int {
	fence := strings.TrimSpace(src[i])[:3]
	r.gap()
	i++
	for ; i < len(src); i++ {
		if strings.HasPrefix(strings.TrimSpace(src[i]), fence) {
			i++
			break
		}
		r.emit(paint(cSoft, cPanel, fit2(" "+src[i], r.w)))
	}
	r.emit("")
	return i
}

// fit2 cuts s to w cells and pads it, keeping its spaces (fit collapses them).
func fit2(s string, w int) string {
	if width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", max(0, w-width(s)))
}

func (r *mdRenderer) paragraph(src []string, i int) int {
	var parts []string
	for ; i < len(src); i++ {
		t := strings.TrimSpace(src[i])
		if t == "" || headingLevel(t) > 0 || isRule(t) || strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") ||
			strings.HasPrefix(t, "|") || strings.HasPrefix(t, ">") || (len(parts) > 0 && listMarker(src[i]) != "") {
			break
		}
		parts = append(parts, t)
	}
	for _, l := range r.wrapSpans(inlineSpans(strings.Join(parts, " "), false), "", "", cInk) {
		r.emit(l)
	}
	r.emit("")
	return i
}

func (r *mdRenderer) quote(src []string, i int) int {
	var parts []string
	for ; i < len(src) && strings.HasPrefix(strings.TrimSpace(src[i]), ">"); i++ {
		parts = append(parts, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src[i]), ">")))
	}
	bar := paint(cLine, cBg, "│ ")
	for _, l := range r.wrapSpans(inlineSpans(strings.Join(parts, " "), false), bar, bar, cMuted) {
		r.emit(l)
	}
	r.emit("")
	return i
}

// listMarker returns the marker a list item line starts with ("- ", "12. "), after its indentation, or "".
func listMarker(line string) string {
	t := strings.TrimLeft(line, " ")
	for _, m := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(t, m) {
			return m
		}
	}
	n := 0
	for n < len(t) && n < 4 && t[n] >= '0' && t[n] <= '9' {
		n++
	}
	if n > 0 && n+1 < len(t) && (t[n] == '.' || t[n] == ')') && t[n+1] == ' ' {
		return t[:n+2]
	}
	return ""
}

func (r *mdRenderer) list(src []string, i int) int {
	for i < len(src) {
		line := src[i]
		m := listMarker(line)
		if m == "" {
			break
		}
		depth := (len(line) - len(strings.TrimLeft(line, " "))) / 2
		text := strings.TrimSpace(strings.TrimLeft(line, " ")[len(m):])
		i++
		// Lines indented under the item, without a marker of their own, continue it.
		for i < len(src) && strings.TrimSpace(src[i]) != "" && listMarker(src[i]) == "" &&
			strings.HasPrefix(src[i], "  ") && !strings.HasPrefix(strings.TrimSpace(src[i]), "```") {
			text += " " + strings.TrimSpace(src[i])
			i++
		}
		bullet := "•"
		if m[0] >= '0' && m[0] <= '9' {
			bullet = strings.TrimSpace(m)
		}
		switch {
		case strings.HasPrefix(text, "[ ] "):
			bullet, text = "☐", text[4:]
		case strings.HasPrefix(text, "[x] "), strings.HasPrefix(text, "[X] "):
			bullet, text = "☑", text[4:]
		}
		indent := strings.Repeat("  ", min(depth, 6))
		first := paint(cInk, cBg, indent) + paint(cGold, cBg, bullet+" ")
		rest := paint(cInk, cBg, indent+strings.Repeat(" ", width(bullet)+1))
		for _, l := range r.wrapSpans(inlineSpans(text, false), first, rest, cInk) {
			r.emit(l)
		}
		if i < len(src) && strings.TrimSpace(src[i]) == "" && i+1 < len(src) && listMarker(src[i+1]) != "" {
			i++ // a loose list: the blank line between items is not the end of it
		}
	}
	r.emit("")
	return i
}

// table lays out consecutive "|" rows in aligned columns. Cells are cut when the table is wider than
// the view.
func (r *mdRenderer) table(src []string, i int) int {
	var rows [][]string
	sepAt := -1
	for ; i < len(src) && strings.HasPrefix(strings.TrimSpace(src[i]), "|"); i++ {
		t := strings.Trim(strings.TrimSpace(src[i]), "|")
		var cells []string
		for _, c := range strings.Split(t, "|") {
			cells = append(cells, strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(c, "**", ""), "`", "")))
		}
		if strings.Trim(t, "|-: ") == "" {
			sepAt = len(rows)
			continue
		}
		rows = append(rows, cells)
	}
	cols := 0
	for _, row := range rows {
		cols = max(cols, len(row))
	}
	if cols == 0 {
		return i
	}
	widths := make([]int, cols)
	for _, row := range rows {
		for c, cell := range row {
			widths[c] = max(widths[c], width(cell))
		}
	}
	// Narrow the widest columns until the table fits: 3 cells of " │ " between columns.
	for sum(widths)+3*(cols-1) > r.w {
		wi := 0
		for c := range widths {
			if widths[c] > widths[wi] {
				wi = c
			}
		}
		if widths[wi] <= 3 {
			break
		}
		widths[wi]--
	}
	r.gap()
	for n, row := range rows {
		var b strings.Builder
		for c := 0; c < cols; c++ {
			if c > 0 {
				b.WriteString(paint(cLine, cBg, " │ "))
			}
			cell := ""
			if c < len(row) {
				cell = row[c]
			}
			if n == 0 && sepAt == 1 {
				b.WriteString(paintBold(cGold, cBg, fit2(cell, widths[c])))
			} else {
				b.WriteString(paint(cInk, cBg, fit2(cell, widths[c])))
			}
		}
		r.emit(b.String())
		if n == 0 && sepAt == 1 {
			var s strings.Builder
			for c := 0; c < cols; c++ {
				if c > 0 {
					s.WriteString("─┼─")
				}
				s.WriteString(strings.Repeat("─", widths[c]))
			}
			r.emit(paint(cLine, cBg, s.String()))
		}
	}
	r.emit("")
	return i
}

func sum(v []int) int {
	n := 0
	for _, x := range v {
		n += x
	}
	return n
}

type spanStyle int

const (
	stPlain spanStyle = iota
	stBold
	stEm
	stCode
	stLink
)

type span struct {
	text  string
	style spanStyle
}

// inlineSpans splits a paragraph into styled runs: **bold**, *em* or _em_, `code`, and [links](url),
// shown as their text. An "_" inside a word (a file name) stays a character. allBold makes plain text
// bold, for headings.
func inlineSpans(s string, allBold bool) []span {
	var out []span
	var cur strings.Builder
	bold, em := false, false
	style := func() spanStyle {
		switch {
		case bold || allBold:
			return stBold
		case em:
			return stEm
		}
		return stPlain
	}
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, span{cur.String(), style()})
			cur.Reset()
		}
	}
	rs := []rune(s)
	boundary := func(i int) bool {
		return i < 0 || i >= len(rs) || rs[i] == ' ' || strings.ContainsRune(".,;:!?()[]\"'", rs[i])
	}
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
		case c == '`':
			end := indexRune(rs, '`', i+1)
			if end < 0 {
				cur.WriteRune(c)
				continue
			}
			flush()
			out = append(out, span{string(rs[i+1 : end]), stCode})
			i = end
		case c == '*' && i+1 < len(rs) && rs[i+1] == '*':
			flush()
			bold = !bold
			i++
		case (c == '*' || c == '_') && (em || (!boundary(i+1) && boundary(i-1))) && (!em || boundary(i+1)):
			flush()
			em = !em
		case c == '[':
			close := indexRune(rs, ']', i+1)
			if close > 0 && close+1 < len(rs) && rs[close+1] == '(' {
				if end := indexRune(rs, ')', close+2); end > 0 {
					flush()
					out = append(out, span{string(rs[i+1 : close]), stLink})
					i = end
					continue
				}
			}
			cur.WriteRune(c)
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return out
}

func indexRune(rs []rune, c rune, from int) int {
	for i := from; i < len(rs); i++ {
		if rs[i] == c {
			return i
		}
	}
	return -1
}

func paintSpan(sp span, text string, base rgb) string {
	switch sp.style {
	case stBold:
		return paintBold(base, cBg, text)
	case stEm:
		return paint(cSoft, cBg, text)
	case stCode:
		return paint(cGoldHi, cPanel, text)
	case stLink:
		return paint(cAsked, cBg, text)
	}
	return paint(base, cBg, text)
}

// wrapSpans fills lines of r.w cells with the words of spans, after the already painted prefixes
// first (the first line) and rest (the others). base colors plain text.
func (r *mdRenderer) wrapSpans(spans []span, first, rest string, base rgb) []string {
	type word struct {
		text  string
		sp    span
		space bool // a space separates it from the word before
	}
	var words []word
	space := false
	for _, sp := range spans {
		parts := strings.Split(sp.text, " ")
		for j, p := range parts {
			if j > 0 {
				space = true
			}
			if p == "" {
				continue
			}
			words = append(words, word{p, sp, space})
			space = false
		}
		if strings.HasSuffix(sp.text, " ") {
			space = true
		}
	}
	var out []string
	prefix := first
	line, used := prefix, width(prefix)
	empty := true
	for _, wd := range words {
		need := width(wd.text)
		if !empty && wd.space {
			need++
		}
		if !empty && used+need > r.w {
			out = append(out, line)
			prefix = rest
			line, used, empty = prefix, width(prefix), true
		}
		text := wd.text
		if !empty && wd.space {
			line += paintSpan(span{style: stPlain}, " ", base)
			used++
		}
		for used+width(text) > r.w && r.w-used > 0 {
			cut := ansi.Truncate(text, r.w-used, "")
			if cut == "" {
				break
			}
			out = append(out, line+paintSpan(wd.sp, cut, base))
			text = text[len(cut):]
			line, used = rest, width(rest)
		}
		line += paintSpan(wd.sp, text, base)
		used += width(text)
		empty = false
	}
	if !empty || len(out) == 0 {
		out = append(out, line)
	}
	return out
}
