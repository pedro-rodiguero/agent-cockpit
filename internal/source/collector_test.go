package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

type fixed struct {
	name     string
	sessions []model.Session
}

func (f fixed) Name() string { return f.name }
func (f fixed) Collect(context.Context, *Env) ([]model.Session, error) {
	return f.sessions, nil
}

func TestCollectorMarksSilentBusySessionsStuckAndSortsByUrgency(t *testing.T) {
	now := time.Now()
	c := &Collector{StuckAfter: 10 * time.Minute, Sources: []Source{fixed{"a", []model.Session{
		{ID: "idle", Name: "a", State: model.Idle},
		{ID: "quiet", Name: "b", State: model.Busy, LastActivity: now.Add(-15 * time.Minute)},
		{ID: "busy", Name: "c", State: model.Busy, LastActivity: now.Add(-time.Minute)},
		{ID: "waiting", Name: "d", State: model.Waiting},
	}}}}
	snap := c.Collect(context.Background())
	var order []string
	for _, s := range snap.Sessions {
		order = append(order, s.ID+"="+string(s.State))
	}
	want := "waiting=waiting quiet=stuck busy=busy idle=idle"
	if got := strings.Join(order, " "); got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	if d := snap.Sessions[1].Detail; !strings.HasPrefix(d, "no activity for 15m") {
		t.Errorf("stuck detail = %q", d)
	}
}

func TestCollectorKeepsToDoTasksInTheirSourcesOrder(t *testing.T) {
	c := &Collector{Sources: []Source{fixed{"a", []model.Session{
		{ID: "z-first", Agent: "todo", Name: "Zebra", State: model.Todo},
		{ID: "idle", Agent: "claude", Name: "x", State: model.Idle},
		{ID: "a-second", Agent: "todo", Name: "Apple", State: model.Todo},
	}}}}
	var order []string
	for _, s := range c.Collect(context.Background()).Sessions {
		order = append(order, s.ID)
	}
	if got := strings.Join(order, " "); got != "idle z-first a-second" {
		t.Errorf("order = %s", got)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
