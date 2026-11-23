package source

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeCollect(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sessions", "4242.json"), `{"pid":4242,"sessionId":"s-1","cwd":"/src/api",
		"name":"api-1","kind":"interactive","status":"waiting","waitingFor":"permission prompt",
		"startedAt":1700000000000,"updatedAt":1700000060000,"statusUpdatedAt":1700000050000,
		"messagingSocketPath":"/tmp/x.sock"}`)
	// A leftover file from a process that is gone, and one whose pid now belongs to something else.
	write(t, filepath.Join(dir, "sessions", "1111.json"), `{"pid":1111,"sessionId":"dead","status":"busy"}`)
	write(t, filepath.Join(dir, "sessions", "2222.json"), `{"pid":2222,"sessionId":"reused","status":"busy"}`)
	write(t, filepath.Join(dir, "projects", "-src-api", "s-1.jsonl"),
		`{"type":"ai-title","aiTitle":"Add rate limiting"}`+"\n"+`{"type":"last-prompt","lastPrompt":"limit  logins\nper IP"}`+"\n")

	env := &Env{Now: time.Now(), Procs: map[int]Proc{
		4242: {PID: 4242, TTY: "ttys004", Args: "claude --resume"},
		2222: {PID: 2222, Args: "vim"},
	}}
	got, err := (&Claude{Dirs: []string{dir}}).Collect(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want 1: %+v", len(got), got)
	}
	s := got[0]
	checks := map[string][2]string{
		"ID":         {s.ID, "claude:s-1"},
		"State":      {string(s.State), "waiting"},
		"Detail":     {s.Detail, "permission prompt"},
		"TTY":        {s.TTY, "ttys004"},
		"Title":      {s.Title, "Add rate limiting"},
		"LastPrompt": {s.LastPrompt, "limit logins per IP"},
		"NativeID":   {s.NativeID, "s-1"},
		"Profile":    {s.Profile, ""},
	}
	for field, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", field, c[0], c[1])
		}
	}
}
