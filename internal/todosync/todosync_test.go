package todosync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
)

// fakeServer answers /api/sync the way cockpit-todo-server does: merge, then send everything back.
func fakeServer(t *testing.T, token string) *httptest.Server {
	var mu sync.Mutex
	var list todo.List
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sync" || r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"Unknown device token."}`))
			return
		}
		var in struct{ Tasks []todo.Task }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("bad sync body: %v", err)
		}
		mu.Lock()
		list.Merge(in.Tasks)
		out := append([]todo.Task{}, list.Tasks...)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string][]todo.Task{"tasks": out})
	}))
}

func newComputer(t *testing.T, url, token string) (*todo.Store, Paths) {
	dir := t.TempDir()
	store := &todo.Store{Path: filepath.Join(dir, "todo.json")}
	p := For(store.Path)
	if err := p.SaveLink(Link{URL: url, Token: token}); err != nil {
		t.Fatal(err)
	}
	return store, p
}

func TestTwoComputersStayInStep(t *testing.T) {
	srv := fakeServer(t, "ckt_good")
	defer srv.Close()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	mac, macSync := newComputer(t, srv.URL, "ckt_good")
	work, workSync := newComputer(t, srv.URL, "ckt_good")
	ctx := context.Background()

	var id string
	_ = mac.Update(now, func(l *todo.List) { id = l.Add("from the mac", "", "", now).ID })
	for _, step := range []func() error{
		func() error { return macSync.SyncNow(ctx, mac, now) },
		func() error { return workSync.SyncNow(ctx, work, now) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	if l, _ := work.Load(); len(l.Open(false)) != 1 {
		t.Fatalf("the other computer did not get the task: %+v", l.Tasks)
	}

	later := now.Add(time.Minute)
	_ = work.Update(later, func(l *todo.List) { l.Delete(id, later) })
	_ = workSync.SyncNow(ctx, work, later)
	_ = macSync.SyncNow(ctx, mac, later)
	if l, _ := mac.Load(); len(l.Open(false)) != 0 {
		t.Errorf("the deletion did not reach the mac: %+v", l.Tasks)
	}
}

func TestSyncIfDueWaitsAndRecords(t *testing.T) {
	srv := fakeServer(t, "ckt_good")
	defer srv.Close()
	now := time.Now()
	store, p := newComputer(t, srv.URL, "ckt_good")
	p.SyncIfDue(context.Background(), store, now)
	first := p.LoadStatus()
	if first.At.IsZero() || first.Error != "" {
		t.Fatalf("first sync: %+v", first)
	}
	p.SyncIfDue(context.Background(), store, now.Add(Every/2))
	if p.LoadStatus().At != first.At {
		t.Error("synced again before it was due")
	}

	_ = p.SaveLink(Link{URL: srv.URL, Token: "ckt_revoked"})
	p.SyncIfDue(context.Background(), store, now.Add(Every))
	if st := p.LoadStatus(); !strings.Contains(st.Error, "Unknown device token") {
		t.Errorf("a refused sync was not recorded: %+v", st)
	}
}

func TestNotConnectedDoesNothing(t *testing.T) {
	store := &todo.Store{Path: filepath.Join(t.TempDir(), "todo.json")}
	p := For(store.Path)
	p.SyncIfDue(context.Background(), store, time.Now())
	if !p.LoadStatus().At.IsZero() {
		t.Error("an unconnected list recorded a sync")
	}
	if err := p.SyncNow(context.Background(), store, time.Now()); err == nil {
		t.Error("SyncNow without a link did not complain")
	}
}

func TestTokenNeverFollowsARedirect(t *testing.T) {
	var leaked bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization") != ""
	}))
	defer elsewhere.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/api/sync", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	store := &todo.Store{Path: filepath.Join(t.TempDir(), "todo.json")}
	err := Sync(context.Background(), Link{URL: redirector.URL, Token: "ckt_secret"}, store, time.Now())
	if err == nil || leaked {
		t.Errorf("redirect followed: err %v, token sent elsewhere %v", err, leaked)
	}
}

func TestCheckURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://todo.example.com":      "https://todo.example.com",
		"https://todo.example.com/":     "https://todo.example.com",
		"http://localhost:8740":         "http://localhost:8740",
		"http://todo.example.com":       "",
		"https://user@todo.example.com": "",
		"https://todo.example.com/x":    "",
		"todo.example.com":              "",
		"ftp://todo.example.com":        "",
	} {
		got, err := CheckURL(raw)
		if (want == "") != (err != nil) || got != want {
			t.Errorf("CheckURL(%q) = %q, %v", raw, got, err)
		}
	}
}

func TestLinkFileIsPrivate(t *testing.T) {
	_, p := newComputer(t, "https://x.test", "ckt_secret")
	fi, err := os.Stat(p.link())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("sync.json is readable by others: %v", fi.Mode())
	}
}
