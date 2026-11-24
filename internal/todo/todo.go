// Package todo is cockpit's own to-do list: a JSON file of tasks the dashboard adds, edits and completes,
// and the tasks source lists in the rail.
//
// The file is meant to be shared with other clients later (a phone app, a sync service), so every task
// carries a stable ID and its creation and change times, and completed tasks stay in the file, with
// their completion time, for a week before they move to a history file next to it.
package todo

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// keepDone is how long a completed task stays in the file before it moves to the history file.
const keepDone = 7 * 24 * time.Hour

// DateLayout is how due dates are written: a calendar day, without a time.
const DateLayout = "2006-01-02"

// Task is one thing to do.
type Task struct {
	ID      string    `json:"id"`
	Text    string    `json:"text"`
	Group   string    `json:"group,omitempty"` // the section it is listed under; Settings.DefaultGroup when empty
	Due     string    `json:"due,omitempty"`   // DateLayout
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Done    time.Time `json:"done,omitzero"` // zero while the task is open
}

// Open reports whether the task still has to be done.
func (t Task) Open() bool { return t.Done.IsZero() }

// Settings change how the list is shown. They are not part of the file: they belong to the machine
// showing the list (see Store.Settings).
type Settings struct {
	// DefaultGroup is the group of a task added without one. "Tasks" when empty.
	DefaultGroup string `json:"defaultGroup,omitempty"`
	// Fold shows the tasks of one group inside another ({"Side project": "Personal"}), marked with their
	// own group's name. With FoldWhenReachable set, only while that host:port accepts connections: a
	// way to say "while I am on the office network".
	Fold              map[string]string `json:"fold,omitempty"`
	FoldWhenReachable string            `json:"foldWhenReachable,omitempty"`
}

// List is the content of the file.
type List struct {
	Settings Settings `json:"-"`
	Tasks    []Task   `json:"tasks"`
}

// DefaultGroup is the group a task without one belongs to.
func (l *List) DefaultGroup() string {
	if l.Settings.DefaultGroup != "" {
		return l.Settings.DefaultGroup
	}
	return "Tasks"
}

// Groups lists the groups in use, in the order they first appear.
func (l *List) Groups() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range l.Tasks {
		g := firstOf(t.Group, l.DefaultGroup())
		if !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	return out
}

// Add appends a task and returns it.
func (l *List) Add(text, group, due string, now time.Time) Task {
	t := Task{ID: newID(), Text: text, Group: group, Due: due, Created: now, Updated: now}
	l.Tasks = append(l.Tasks, t)
	return t
}

// Edit changes a task's text, group and due date.
func (l *List) Edit(id, text, group, due string, now time.Time) bool {
	return l.change(id, func(t *Task) { t.Text, t.Group, t.Due, t.Updated = text, group, due, now })
}

// Complete marks a task done.
func (l *List) Complete(id string, now time.Time) bool {
	return l.change(id, func(t *Task) { t.Done, t.Updated = now, now })
}

// Reopen marks a done task open again.
func (l *List) Reopen(id string, now time.Time) bool {
	return l.change(id, func(t *Task) { t.Done, t.Updated = time.Time{}, now })
}

// Delete removes a task.
func (l *List) Delete(id string) bool {
	for i, t := range l.Tasks {
		if t.ID == id {
			l.Tasks = append(l.Tasks[:i], l.Tasks[i+1:]...)
			return true
		}
	}
	return false
}

// Find returns the task with id.
func (l *List) Find(id string) (Task, bool) {
	for _, t := range l.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

func (l *List) change(id string, fn func(*Task)) bool {
	for i := range l.Tasks {
		if l.Tasks[i].ID == id {
			fn(&l.Tasks[i])
			return true
		}
	}
	return false
}

// Store reads and writes a List in a file. Writes replace the file atomically.
type Store struct {
	Path string
	// Settings, when set, gives the settings each loaded list is shown with.
	Settings func() Settings
	mu       sync.Mutex
}

// DefaultPath is where the list lives: <user config dir>/agent-cockpit/todo.json.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "agent-cockpit", "todo.json")
}

// Load reads the list. A file that does not exist yet is an empty list.
func (s *Store) Load() (List, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() (List, error) {
	var l List
	b, err := os.ReadFile(s.Path)
	if err == nil {
		err = json.Unmarshal(b, &l)
	} else if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	if s.Settings != nil {
		l.Settings = s.Settings()
	}
	return l, err
}

// Update loads the list, applies fn and saves the result, moving tasks completed more than a week
// before now to the history file.
func (s *Store) Update(now time.Time, fn func(*List)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.load()
	if err != nil {
		return err
	}
	fn(&l)
	var keep, old []Task
	for _, t := range l.Tasks {
		if !t.Open() && now.Sub(t.Done) > keepDone {
			old = append(old, t)
		} else {
			keep = append(keep, t)
		}
	}
	if err := s.archive(old); err != nil {
		return err
	}
	l.Tasks = keep
	return s.save(l)
}

// HistoryPath is the file completed tasks move to: one JSON task per line.
func (s *Store) HistoryPath() string { return strings.TrimSuffix(s.Path, ".json") + "-done.jsonl" }

func (s *Store) archive(tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}
	f, err := os.OpenFile(s.HistoryPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, t := range tasks {
		b, _ := json.Marshal(t)
		w.Write(append(b, '\n'))
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s *Store) save(l List) error {
	if l.Tasks == nil {
		l.Tasks = []Task{}
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Shown is an open task as it is listed: in its group, or in the group it folds into, in which case
// From names its own group.
type Shown struct {
	Task
	Group string
	From  string
}

// Open lists the open tasks to show, grouped (groups in the order they first appear) and, within a
// group, by due date, the ones without a date last, then in the order they were added. folded says
// whether Settings.Fold applies.
func (l *List) Open(folded bool) []Shown {
	var out []Shown
	for _, t := range l.Tasks {
		if !t.Open() {
			continue
		}
		s := Shown{Task: t, Group: firstOf(t.Group, l.DefaultGroup())}
		if into, ok := l.Settings.Fold[s.Group]; ok && folded {
			s.From, s.Group = s.Group, into
		}
		out = append(out, s)
	}
	rank := map[string]int{}
	for _, s := range out {
		if _, ok := rank[s.Group]; !ok {
			rank[s.Group] = len(rank)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if rank[a.Group] != rank[b.Group] {
			return rank[a.Group] < rank[b.Group]
		}
		if (a.Due == "") != (b.Due == "") {
			return b.Due == ""
		}
		return a.Due < b.Due
	})
	return out
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "t" + hex.EncodeToString(b)
}

func firstOf(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
