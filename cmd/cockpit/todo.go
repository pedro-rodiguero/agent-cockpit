package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
)

const todoUsage = `cockpit todo: the to-do list, for scripts and chat bots

  cockpit todo [-json]                 open tasks, numbered as listed
  cockpit todo add <text>              add a task; #group and @date are optional, as in the dashboard
  cockpit todo done <n|id>...          complete tasks, by number from the last listing or by id
  cockpit todo reopen [id]             reopen a task: the one completed last when no id is given

Numbers follow the listing's order, which changes as tasks come and go: done prints each task it
completes, so a stale number shows.
`

// listedTask is a task in the output of `cockpit todo -json`.
type listedTask struct {
	N     int    `json:"n"`
	ID    string `json:"id"`
	Text  string `json:"text"`
	Group string `json:"group"`
	Due   string `json:"due,omitempty"`
}

func runTodo(args []string) error {
	file := settings()
	store := &todo.Store{Path: todo.DefaultPath(), Settings: func() todo.Settings { return file.Current().Todo }}
	return todoCommand(store, args, os.Stdout, time.Now())
}

func todoCommand(store *todo.Store, args []string, out io.Writer, now time.Time) error {
	cmd := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "list", "ls":
		fs := flag.NewFlagSet("todo", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "")
		fs.SetOutput(io.Discard)
		if err := fs.Parse(args); err != nil {
			return errors.New(todoUsage)
		}
		l, err := store.Load()
		if err != nil {
			return err
		}
		return printTasks(out, numbered(l), *asJSON, now)
	case "add":
		line := strings.TrimSpace(strings.Join(args, " "))
		var added todo.Task
		err := store.Update(now, func(l *todo.List) {
			text, group, due := todo.Parse(line, l.Groups(), now)
			if text != "" {
				added = l.Add(text, group, due, now)
			}
		})
		if err != nil {
			return err
		}
		if added.ID == "" {
			return errors.New("nothing to add: the task has no text")
		}
		fmt.Fprintln(out, "added:", describe(added, store, now))
		return nil
	case "done":
		if len(args) == 0 {
			return errors.New("usage: cockpit todo done <n|id>...")
		}
		var done []todo.Task
		var missing []string
		err := store.Update(now, func(l *todo.List) {
			listed := numbered(*l)
			for _, ref := range args {
				t, ok := find(*l, listed, ref)
				if !ok || !t.Open() {
					missing = append(missing, ref)
					continue
				}
				l.Complete(t.ID, now)
				done = append(done, t)
			}
		})
		if err != nil {
			return err
		}
		for _, t := range done {
			fmt.Fprintln(out, "done:", t.Text)
		}
		if len(missing) > 0 {
			return fmt.Errorf("no open task %s", strings.Join(missing, ", "))
		}
		return nil
	case "reopen", "undo":
		var reopened todo.Task
		err := store.Update(now, func(l *todo.List) {
			t, ok := lastDone(*l)
			if len(args) > 0 {
				t, ok = l.Find(args[0])
			}
			if ok && !t.Open() {
				l.Reopen(t.ID, now)
				reopened = t
			}
		})
		if err != nil {
			return err
		}
		if reopened.ID == "" {
			return errors.New("no completed task to reopen")
		}
		fmt.Fprintln(out, "reopened:", reopened.Text)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(out, todoUsage)
		return nil
	}
	return errors.New(todoUsage)
}

// numbered is the open tasks in the order they are listed, numbered from 1.
func numbered(l todo.List) []listedTask {
	var out []listedTask
	for i, s := range l.Open(false) {
		out = append(out, listedTask{N: i + 1, ID: s.ID, Text: s.Text, Group: s.Group, Due: s.Due})
	}
	return out
}

// find resolves a reference to a task: a number from the listing, or an id.
func find(l todo.List, listed []listedTask, ref string) (todo.Task, bool) {
	if n, err := strconv.Atoi(ref); err == nil {
		if n < 1 || n > len(listed) {
			return todo.Task{}, false
		}
		ref = listed[n-1].ID
	}
	return l.Find(ref)
}

// lastDone is the task completed most recently.
func lastDone(l todo.List) (todo.Task, bool) {
	var last todo.Task
	for _, t := range l.Tasks {
		if t.Live() && !t.Open() && t.Done.After(last.Done) {
			last = t
		}
	}
	return last, last.ID != ""
}

func printTasks(out io.Writer, tasks []listedTask, asJSON bool, now time.Time) error {
	if asJSON {
		if tasks == nil {
			tasks = []listedTask{}
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(tasks)
	}
	if len(tasks) == 0 {
		fmt.Fprintln(out, "nothing to do")
		return nil
	}
	group := ""
	for _, t := range tasks {
		if t.Group != group {
			if group != "" {
				fmt.Fprintln(out)
			}
			group = t.Group
			fmt.Fprintln(out, strings.ToUpper(group))
		}
		line := fmt.Sprintf("%3d  %s", t.N, t.Text)
		if t.Due != "" {
			line += "  · " + dueLabel(t.Due, now)
		}
		fmt.Fprintln(out, line)
	}
	return nil
}

// describe is a task as add reports it: its text, group and due date.
func describe(t todo.Task, store *todo.Store, now time.Time) string {
	group := t.Group
	if group == "" {
		l, _ := store.Load()
		group = l.DefaultGroup()
	}
	s := t.Text + " · " + group
	if t.Due != "" {
		s += " · " + dueLabel(t.Due, now)
	}
	return s
}

// dueLabel is a due day relative to now: today, tomorrow, in 3d, 2d late.
func dueLabel(day string, now time.Time) string {
	d, err := time.ParseInLocation(todo.DateLayout, day, now.Location())
	if err != nil {
		return day
	}
	y, m, dd := now.Date()
	days := int(math.Round(d.Sub(time.Date(y, m, dd, 0, 0, 0, 0, now.Location())).Hours() / 24))
	switch {
	case days < 0:
		return fmt.Sprintf("%dd late", -days)
	case days == 0:
		return "today"
	case days == 1:
		return "tomorrow"
	}
	return fmt.Sprintf("in %dd", days)
}
