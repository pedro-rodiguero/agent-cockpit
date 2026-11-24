package source

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
	"github.com/pedro-rodiguero/agent-cockpit/internal/todosync"
)

// TasksName is the tasks source's name and the prefix of its session IDs.
const TasksName = "tasks"

// How the tasks source checks Settings.FoldWhenReachable: a connection attempt this long, remembered
// for this long, so a collection pass rarely waits on the network.
const (
	reachTimeout = 300 * time.Millisecond
	reachTTL     = 30 * time.Second
)

// Tasks lists the open tasks of cockpit's own to-do list.
type Tasks struct {
	Store *todo.Store

	mu        sync.Mutex
	reachAddr string
	reachAt   time.Time
	reachable bool
}

func (t *Tasks) Name() string { return TasksName }

func (t *Tasks) Collect(ctx context.Context, env *Env) ([]model.Session, error) {
	todosync.For(t.Store.Path).SyncIfDue(ctx, t.Store, env.Now)
	l, err := t.Store.Load()
	if err != nil {
		return nil, err
	}
	folded := len(l.Settings.Fold) > 0
	if addr := l.Settings.FoldWhenReachable; folded && addr != "" {
		folded = t.reach(ctx, addr, env.Now)
	}
	var out []model.Session
	for _, s := range l.Open(folded) {
		out = append(out, model.Session{
			ID:      TasksName + ":" + s.ID,
			Agent:   TasksName,
			Name:    s.Text,
			State:   model.Todo,
			Group:   s.Group,
			Detail:  s.From,
			Due:     dueTime(s.Due),
			Started: s.Created,
		})
	}
	return out, nil
}

// reach reports whether addr accepts connections, from a result at most reachTTL old.
func (t *Tasks) reach(ctx context.Context, addr string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if addr == t.reachAddr && now.Sub(t.reachAt) < reachTTL {
		return t.reachable
	}
	d := net.Dialer{Timeout: reachTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err == nil {
		conn.Close()
	}
	t.reachAddr, t.reachAt, t.reachable = addr, now, err == nil
	return t.reachable
}

// dueTime is a due day as a time: noon, local time, so it reads as that day in any zone nearby.
func dueTime(day string) time.Time {
	d, err := time.ParseInLocation(todo.DateLayout, day, time.Local)
	if err != nil {
		return time.Time{}
	}
	return d.Add(12 * time.Hour)
}
