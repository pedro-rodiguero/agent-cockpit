package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
	"github.com/pedro-rodiguero/agent-cockpit/internal/todosync"
)

func TestTodoCommand(t *testing.T) {
	store := &todo.Store{Path: filepath.Join(t.TempDir(), "todo.json"),
		Settings: func() todo.Settings { return todo.Settings{DefaultGroup: "Personal"} }}
	now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.Local)
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := todoCommand(store, args, &out, now)
		return out.String(), err
	}

	if out, err := run("add", "call", "the", "venue", "@tomorrow"); err != nil || out != "added: call the venue · Personal · tomorrow\n" {
		t.Fatalf("add: %q %v", out, err)
	}
	if _, err := run("add", "ship", "release", "#Work"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("add", "#Work"); err == nil {
		t.Error("a task with no text was added")
	}
	out, _ := run()
	want := "PERSONAL\n  1  call the venue  · tomorrow\n\nWORK\n  2  ship release\n"
	if out != want {
		t.Errorf("list:\n%s\nwant:\n%s", out, want)
	}

	var listed []listedTask
	out, _ = run("-json")
	if err := json.Unmarshal([]byte(out), &listed); err != nil || len(listed) != 2 || listed[1].Group != "Work" {
		t.Fatalf("json: %s %v", out, err)
	}

	if out, err := run("done", "2", "9"); err == nil || out != "done: ship release\n" {
		t.Errorf("done: %q %v, want ship release done and 9 reported", out, err)
	}
	if out, err := run("reopen"); err != nil || out != "reopened: ship release\n" {
		t.Errorf("reopen: %q %v", out, err)
	}
	if out, err := run("done", listed[0].ID); err != nil || !strings.Contains(out, "call the venue") {
		t.Errorf("done by id: %q %v", out, err)
	}
	if out, _ := run("list"); out != "WORK\n  1  ship release\n" {
		t.Errorf("after done: %q", out)
	}
}

func TestDueLabel(t *testing.T) {
	now := time.Date(2026, 11, 24, 23, 30, 0, 0, time.Local)
	for day, want := range map[string]string{"2026-11-24": "today", "2026-11-25": "tomorrow", "2026-12-01": "in 7d", "2026-11-21": "3d late"} {
		if got := dueLabel(day, now); got != want {
			t.Errorf("dueLabel(%s) = %q, want %q", day, got, want)
		}
	}
}

func TestTodoStatus(t *testing.T) {
	store := &todo.Store{Path: filepath.Join(t.TempDir(), "todo.json")}
	now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.Local)
	var out bytes.Buffer
	if err := todoCommand(store, []string{"status", "-json"}, &out, now); err != nil || !strings.Contains(out.String(), `"connected":false`) {
		t.Fatalf("not connected: %s %v", out.String(), err)
	}
	paths := todosync.For(store.Path)
	if err := paths.SaveLink(todosync.Link{URL: "https://todo.test", Token: "ckt_secret"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	_ = todoCommand(store, []string{"status", "-json"}, &out, now)
	if s := out.String(); !strings.Contains(s, `"connected":true`) || !strings.Contains(s, "https://todo.test") || strings.Contains(s, "ckt_secret") {
		t.Errorf("connected status: %s", s)
	}
	out.Reset()
	_ = todoCommand(store, []string{"status"}, &out, now)
	if !strings.Contains(out.String(), "not synced yet") {
		t.Errorf("text status: %s", out.String())
	}
}
