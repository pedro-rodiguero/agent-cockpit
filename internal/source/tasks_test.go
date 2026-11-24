package source

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
)

func TestTasksFoldsWhileTheHostAnswers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	store := &todo.Store{Path: filepath.Join(t.TempDir(), "todo.json")}
	now := time.Now()
	if err := store.Update(now, func(l *todo.List) {
		l.Settings.Fold = map[string]string{"Side": "Personal"}
		l.Settings.FoldWhenReachable = ln.Addr().String()
		l.Add("site", "Side", "2026-11-26", now)
	}); err != nil {
		t.Fatal(err)
	}
	src := &Tasks{Store: store}
	got, err := src.Collect(context.Background(), &Env{Now: now})
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	s := got[0]
	if s.State != model.Todo || s.Group != "Personal" || s.Detail != "Side" || s.Due.Format("2006-01-02 15") != "2026-11-26 12" {
		t.Errorf("folded task: %+v", s)
	}

	ln.Close()
	got, _ = src.Collect(context.Background(), &Env{Now: now.Add(reachTTL)})
	if got[0].Group != "Side" || got[0].Detail != "" {
		t.Errorf("unreachable host still folds: %+v", got[0])
	}
}
