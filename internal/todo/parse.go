package todo

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Parse reads a task as typed in one line: "#group" picks the group (an existing one, whatever its case,
// or a new one) and "@date" the due date; the remaining words are the text.
//
// Dates: @today, @tomorrow (also @hoje, @amanhã), @25/12, @25/12/2027, @2027-12-25. A day and month
// without a year is the next such day from now. Words that are not recognized stay in the text.
func Parse(line string, groups []string, now time.Time) (text, group, due string) {
	var words []string
	for _, w := range strings.Fields(line) {
		switch {
		case len(w) > 1 && w[0] == '#':
			group = matchGroup(w[1:], groups)
		case len(w) > 1 && w[0] == '@':
			if d, ok := parseDate(w[1:], now); ok {
				due = d
			} else {
				words = append(words, w)
			}
		default:
			words = append(words, w)
		}
	}
	return strings.Join(words, " "), group, due
}

// Format writes a task back as a line Parse reads, for editing it.
func Format(t Task) string {
	parts := []string{t.Text}
	if t.Group != "" {
		parts = append(parts, "#"+strings.ReplaceAll(t.Group, " ", "_"))
	}
	if t.Due != "" {
		parts = append(parts, "@"+t.Due)
	}
	return strings.Join(parts, " ")
}

func matchGroup(name string, groups []string) string {
	name = strings.ReplaceAll(name, "_", " ")
	for _, g := range groups {
		if strings.EqualFold(g, name) {
			return g
		}
	}
	r, n := utf8.DecodeRuneInString(name)
	return string(unicode.ToUpper(r)) + name[n:]
}

func parseDate(s string, now time.Time) (string, bool) {
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	switch strings.ToLower(s) {
	case "today", "hoje":
		return today.Format(DateLayout), true
	case "tomorrow", "amanhã", "amanha":
		return today.AddDate(0, 0, 1).Format(DateLayout), true
	}
	if t, err := time.ParseInLocation(DateLayout, s, now.Location()); err == nil {
		return t.Format(DateLayout), true
	}
	if t, err := time.ParseInLocation("2/1/2006", s, now.Location()); err == nil {
		return t.Format(DateLayout), true
	}
	if t, err := time.ParseInLocation("2/1", s, now.Location()); err == nil {
		t = t.AddDate(y-t.Year(), 0, 0)
		if t.Before(today) {
			t = t.AddDate(1, 0, 0)
		}
		return t.Format(DateLayout), true
	}
	return "", false
}
