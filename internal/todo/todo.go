// Package todo is cockpit's own to-do list: a JSON file of tasks the dashboard adds, edits and completes,
// and the tasks source lists in the rail.
//
// The file is shared with other clients (the to-do server and its phone app), so every task carries a
// stable ID and its creation and change times, and a deleted task stays in the file as a tombstone:
// Merge then tells a deletion from a task the other side has not seen yet. Completed tasks stay for a
// week before they move to a history file next to the list; tombstones are dropped after a week.
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

	"github.com/pedro-rodiguero/agent-cockpit/internal/filelock"
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
	Done    time.Time `json:"done,omitzero"`    // zero while the task is open
	Deleted time.Time `json:"deleted,omitzero"` // set when the task was deleted; zero otherwise
}

// Live reports whether the task has not been deleted.
func (t Task) Live() bool { return t.Deleted.IsZero() }

// Open reports whether the task still has to be done.
func (t Task) Open() bool { return t.Done.IsZero() && t.Live() }

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
		if !t.Live() {
			continue
		}
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

// Delete marks a task deleted, leaving a tombstone for other copies of the list to learn from.
func (l *List) Delete(id string, now time.Time) bool {
	return l.change(id, func(t *Task) { t.Deleted, t.Updated = now, now })
}

// Find returns the task with id, unless it was deleted.
func (l *List) Find(id string) (Task, bool) {
	for _, t := range l.Tasks {
		if t.ID == id && t.Live() {
			return t, true
		}
	}
	return Task{}, false
}

// Merge takes in another copy's tasks: for each task, the version changed last wins, and on a tie the
// incoming one. Tasks new to the list are added after the ones it has, in their own order.
func (l *List) Merge(incoming []Task) {
	at := make(map[string]int, len(l.Tasks))
	for i, t := range l.Tasks {
		at[t.ID] = i
	}
	for _, t := range incoming {
		if t.ID == "" {
			continue
		}
		i, ok := at[t.ID]
		switch {
		case !ok:
			at[t.ID] = len(l.Tasks)
			l.Tasks = append(l.Tasks, t)
		case !t.Updated.Before(l.Tasks[i].Updated):
			l.Tasks[i] = t
		}
	}
}

func (l *List) change(id string, fn func(*Task)) bool {
	for i := range l.Tasks {
		if l.Tasks[i].ID == id && l.Tasks[i].Live() {
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
	unlock, err := s.lockFile()
	if err != nil {
		return err
	}
	defer unlock()
	l, err := s.load()
	if err != nil {
		return err
	}
	fn(&l)
	var keep, old []Task
	for _, t := range l.Tasks {
		switch {
		case !t.Live():
			if now.Sub(t.Deleted) <= keepDone {
				keep = append(keep, t)
			}
		case !t.Open() && now.Sub(t.Done) > keepDone:
			old = append(old, t)
		default:
			keep = append(keep, t)
		}
	}
	if err := s.archive(old); err != nil {
		return err
	}
	l.Tasks = keep
	return s.save(l)
}

// lockFile holds an exclusive lock on a file next to the list until unlock is called, so processes
// that change the list (dashboards, `cockpit todo`, the server) take turns instead of overwriting each
// other's changes.
func (s *Store) lockFile() (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := filelock.Lock(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = filelock.Unlock(f)
		f.Close()
	}, nil
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
