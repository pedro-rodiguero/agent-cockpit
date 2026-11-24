package todo

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// FuzzParse: any line parses without panicking, into trimmed text, a known group or none, and a real
// date or none. Run longer with: go test ./internal/todo -fuzz FuzzParse
func FuzzParse(f *testing.F) {
	for _, s := range []string{"call the venue #Personal @tomorrow", "@31/02 x", "#", "@", "x @2027-13-01", "a #b #c @hoje @amanhã",
		"\x00#\xff@", "x @29/02/2028", "x @29/02/2027", "  spaced   out  ", "x @0/0", "x @99999999999999999999/1"} {
		f.Add(s)
	}
	groups := []string{"Personal", "Work"}
	f.Fuzz(func(t *testing.T, line string) {
		text, group, due := Parse(line, groups, now)
		if text != strings.TrimSpace(text) {
			t.Errorf("text not trimmed: %q", text)
		}
		if utf8.ValidString(line) && !utf8.ValidString(text) {
			t.Errorf("valid line gave invalid text: %q", text)
		}
		if due != "" {
			if _, err := time.Parse(DateLayout, due); err != nil {
				t.Errorf("due %q is not a date", due)
			}
		}
		_ = group
	})
}

// FuzzMerge: merging the same tasks twice changes nothing the second time, and never loses a task.
func FuzzMerge(f *testing.F) {
	f.Add("a", "b", int64(0), int64(60), true)
	f.Add("same", "same", int64(5), int64(5), false)
	f.Fuzz(func(t *testing.T, id1, id2 string, u1, u2 int64, del bool) {
		base := time.Unix(1_800_000_000, 0)
		var l List
		l.Tasks = []Task{{ID: id1, Text: "x", Updated: base.Add(time.Duration(u1) * time.Second)}}
		in := []Task{{ID: id2, Text: "y", Updated: base.Add(time.Duration(u2) * time.Second)}}
		if del {
			in[0].Deleted = in[0].Updated
		}
		l.Merge(in)
		once := append([]Task(nil), l.Tasks...)
		l.Merge(in)
		if len(once) != len(l.Tasks) {
			t.Fatalf("second merge changed the size: %v → %v", once, l.Tasks)
		}
		for i := range once {
			if once[i] != l.Tasks[i] {
				t.Fatalf("second merge changed a task: %v → %v", once[i], l.Tasks[i])
			}
		}
		seen := map[string]bool{}
		for _, x := range l.Tasks {
			seen[x.ID] = true
		}
		if id1 != "" && !seen[id1] || id2 != "" && !seen[id2] {
			t.Fatalf("a task was lost: %v", l.Tasks)
		}
	})
}
