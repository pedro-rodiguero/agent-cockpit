package main

import (
	"bufio"
	"context"
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

	"golang.org/x/term"

	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
	"github.com/pedro-rodiguero/agent-cockpit/internal/todosync"
)

const todoUsage = `cockpit todo: the to-do list, for scripts and chat bots

  cockpit todo [-json]                 open tasks, numbered as listed
  cockpit todo add <text>              add a task; #group and @date are optional, as in the dashboard
  cockpit todo done <n|id>...          complete tasks, by number from the last listing or by id
  cockpit todo reopen [id]             reopen a task: the one completed last when no id is given
  cockpit todo connect <server>        sync with a cockpit-todo-server; asks for a device token from its app
  cockpit todo sync                    sync now and say how the last sync went
  cockpit todo status [-json]          whether this computer syncs, where to, and how the last sync went
  cockpit todo disconnect              stop syncing

Numbers follow the listing's order, which changes as tasks come and go: done prints each task it
completes, so a stale number shows.
`

// errUsage is a wrong use of the command; its text is the usage.
var errUsage = errors.New(strings.TrimSpace(todoUsage))

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
			return errUsage
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
			return errors.New("usage: cockpit todo done <n|id> [<n|id>…]")
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
	case "connect":
		return connect(store, args, out, now)
	case "sync":
		paths := todosync.For(store.Path)
		if err := paths.SyncNow(context.Background(), store, now); err != nil {
			return err
		}
		link, _, _ := paths.LoadLink()
		fmt.Fprintln(out, "synced with", link.URL)
		return nil
	case "status":
		return syncStatus(store, args, out, now)
	case "disconnect":
		if err := todosync.For(store.Path).Disconnect(); err != nil {
			return err
		}
		fmt.Fprintln(out, "this computer no longer syncs its to-do list")
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(out, todoUsage)
		return nil
	}
	return errUsage
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

// connect links the list to a server: it asks for a device token (made in the server's app, under
// Settings › Devices), syncs once with it, and keeps it only when that worked.
func connect(store *todo.Store, args []string, out io.Writer, now time.Time) error {
	if len(args) != 1 {
		return errors.New("usage: cockpit todo connect https://todo.example.com")
	}
	url, err := todosync.CheckURL(args[0])
	if err != nil {
		return err
	}
	token, err := readSecret("Device token (from the app: Settings › Devices › Add): ")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(token, "ckt_") {
		return errors.New("that is not a device token: they start with ckt_")
	}
	link := todosync.Link{URL: url, Token: token}
	if err := todosync.Sync(context.Background(), link, store, now); err != nil {
		return err
	}
	paths := todosync.For(store.Path)
	if err := paths.SaveLink(link); err != nil {
		return err
	}
	paths.SyncNow(context.Background(), store, now) //nolint:errcheck // it just worked; this records it
	fmt.Fprintln(out, "connected: the to-do list syncs with", url, "every 30 seconds while cockpit runs")
	return nil
}

// readSecret reads a line without echo on a terminal, or plainly from a pipe.
func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(b)), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// syncState is `cockpit todo status -json`, which the menu bar app's Settings window reads.
type syncState struct {
	Connected bool      `json:"connected"`
	URL       string    `json:"url,omitempty"`
	LastSync  time.Time `json:"lastSync,omitzero"`
	Error     string    `json:"error,omitempty"`
}

func syncStatus(store *todo.Store, args []string, out io.Writer, now time.Time) error {
	paths := todosync.For(store.Path)
	link, ok, err := paths.LoadLink()
	if err != nil {
		return err
	}
	st := syncState{Connected: ok, URL: link.URL}
	if ok {
		last := paths.LoadStatus()
		st.LastSync, st.Error = last.At, last.Error
	}
	if len(args) > 0 && args[0] == "-json" {
		return json.NewEncoder(out).Encode(st)
	}
	switch {
	case !st.Connected:
		fmt.Fprintln(out, "not connected: run cockpit todo connect <server>")
	case st.LastSync.IsZero():
		fmt.Fprintln(out, "connected to", st.URL, "· not synced yet")
	case st.Error != "":
		fmt.Fprintf(out, "connected to %s · the last sync, %s ago, failed: %s\n", st.URL, now.Sub(st.LastSync).Round(time.Second), st.Error)
	default:
		fmt.Fprintf(out, "connected to %s · synced %s ago\n", st.URL, now.Sub(st.LastSync).Round(time.Second))
	}
	return nil
}
