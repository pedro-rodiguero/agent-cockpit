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
	if !l.Reopen(done.ID, now) || len(l.Open(false)) != 5 || !l.Delete(soon.ID, now) || len(l.Open(false)) != 4 {
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

func TestDeleteLeavesATombstone(t *testing.T) {
	var l List
	a := l.Add("a", "", "", now)
	l.Add("b", "Side", "", now)
	if !l.Delete(a.ID, now.Add(time.Minute)) || l.Delete(a.ID, now) {
		t.Fatal("delete: first should succeed, second should find nothing")
	}
	if _, ok := l.Find(a.ID); ok || len(l.Open(false)) != 1 || len(l.Tasks) != 2 {
		t.Errorf("deleted task still listed, or its tombstone is gone: %+v", l.Tasks)
	}
	if l.Edit(a.ID, "back", "", "", now) || l.Complete(a.ID, now) {
		t.Error("a deleted task changed")
	}
	if g := l.Groups(); len(g) != 1 || g[0] != "Side" {
		t.Errorf("groups of a deleted task still listed: %v", g)
	}
}

func TestMerge(t *testing.T) {
	t0 := now
	var mine List
	a := mine.Add("a", "", "", t0)
	b := mine.Add("b", "", "", t0)

	theirs := []Task{a, b}
	theirs[0].Text, theirs[0].Updated = "a, edited there", t0.Add(2*time.Minute) // newer there
	theirs[1].Deleted, theirs[1].Updated = t0.Add(time.Minute), t0.Add(time.Minute)
	c := Task{ID: "tc", Text: "c", Created: t0, Updated: t0}
	theirs = append(theirs, c)
	mine.Edit(b.ID, "b, edited here", "", "", t0.Add(3*time.Minute)) // newer here than the deletion

	mine.Merge(theirs)
	got := map[string]Task{}
	for _, x := range mine.Tasks {
		got[x.ID] = x
	}
	if got[a.ID].Text != "a, edited there" {
		t.Errorf("newer remote edit lost: %+v", got[a.ID])
	}
	if !got[b.ID].Live() || got[b.ID].Text != "b, edited here" {
		t.Errorf("older remote deletion won over a newer local edit: %+v", got[b.ID])
	}
	if len(mine.Tasks) != 3 || mine.Tasks[2].ID != "tc" {
		t.Errorf("new task not appended: %+v", mine.Tasks)
	}

	// Merging is idempotent and commutative in what survives.
	again := mine
	again.Tasks = append([]Task(nil), mine.Tasks...)
	again.Merge(mine.Tasks)
	if len(again.Tasks) != 3 {
		t.Errorf("merging a copy into itself changed it: %+v", again.Tasks)
	}
}

func TestStoreDropsOldTombstones(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), "todo.json")}
	var id string
	if err := s.Update(now, func(l *List) { id = l.Add("x", "", "", now).ID; l.Delete(id, now) }); err != nil {
		t.Fatal(err)
	}
	l, _ := s.Load()
	if len(l.Tasks) != 1 {
		t.Fatalf("tombstone not kept: %+v", l.Tasks)
	}
	if err := s.Update(now.Add(8*24*time.Hour), func(*List) {}); err != nil {
		t.Fatal(err)
	}
	l, _ = s.Load()
	if len(l.Tasks) != 0 {
		t.Errorf("old tombstone kept: %+v", l.Tasks)
	}
	if b, _ := os.ReadFile(s.HistoryPath()); len(b) != 0 {
		t.Errorf("a deleted task went to the history: %s", b)
	}
}
