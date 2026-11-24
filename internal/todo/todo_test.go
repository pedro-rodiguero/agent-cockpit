package todo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 24, 10, 0, 0, 0, time.Local)

func TestParse(t *testing.T) {
	groups := []string{"Personal", "Side project"}
	for _, tc := range []struct{ in, text, group, due string }{
		{"call the venue @tomorrow #personal", "call the venue", "Personal", "2026-11-25"},
		{"call the caterer @amanhã", "call the caterer", "", "2026-11-25"},
		{"ship it #side_project @25/12", "ship it", "Side project", "2026-12-25"},
		{"renew passport @3/1", "renew passport", "", "2027-01-03"},
		{"pay @2026-11-30 #work", "pay", "Work", "2026-11-30"},
		{"email @ana about it", "email @ana about it", "", ""},
	} {
		text, group, due := Parse(tc.in, groups, now)
		if text != tc.text || group != tc.group || due != tc.due {
			t.Errorf("Parse(%q) = %q %q %q, want %q %q %q", tc.in, text, group, due, tc.text, tc.group, tc.due)
		}
	}
	task := Task{Text: "ship it", Group: "Side project", Due: "2026-12-25"}
	if text, group, due := Parse(Format(task), groups, now); text != task.Text || group != task.Group || due != task.Due {
		t.Errorf("Format does not round-trip: %q", Format(task))
	}
}

func TestOpenGroupsSortsAndFolds(t *testing.T) {
	var l List
	l.Settings.Fold = map[string]string{"Side project": "Personal"}
	l.Add("later", "Personal", "", now)
	l.Add("site", "Side project", "2026-11-26", now)
	soon := l.Add("soon", "Personal", "2026-11-25", now)
	l.Add("default", "", "", now)
	done := l.Add("done", "Personal", "", now)
	l.Complete(done.ID, now)

	show := func(s []Shown) string {
		var out []string
		for _, x := range s {
			out = append(out, x.Group+":"+x.Text+":"+x.From)
		}
		return strings.Join(out, " ")
	}
	if got, want := show(l.Open(false)), "Personal:soon: Personal:later: Side project:site: Tasks:default:"; got != want {
		t.Errorf("unfolded:\n got %s\nwant %s", got, want)
	}
	if got, want := show(l.Open(true)), "Personal:soon: Personal:site:Side project Personal:later: Tasks:default:"; got != want {
		t.Errorf("folded:\n got %s\nwant %s", got, want)
	}
	if !l.Reopen(done.ID, now) || len(l.Open(false)) != 5 || !l.Delete(soon.ID) || len(l.Open(false)) != 4 {
		t.Error("reopen or delete did not work")
	}
}

func TestStoreArchivesOldDoneTasks(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), "todo.json")}
	if l, err := s.Load(); err != nil || len(l.Tasks) != 0 {
		t.Fatalf("missing file: %v %v", l, err)
	}
	var old, recent Task
	if err := s.Update(now, func(l *List) {
		old, recent = l.Add("old", "", "", now), l.Add("recent", "", "", now)
		l.Add("open", "", "", now)
		l.Complete(old.ID, now.Add(-8*24*time.Hour))
		l.Complete(recent.ID, now.Add(-time.Hour))
	}); err != nil {
		t.Fatal(err)
	}
	l, err := s.Load()
	if err != nil || len(l.Tasks) != 2 {
		t.Fatalf("after update: %d tasks, %v", len(l.Tasks), err)
	}
	if _, ok := l.Find(old.ID); ok {
		t.Error("old done task still in the list")
	}
	hist, _ := os.ReadFile(s.HistoryPath())
	if !strings.Contains(string(hist), old.ID) || strings.Count(string(hist), "\n") != 1 {
		t.Errorf("history: %s", hist)
	}
}
