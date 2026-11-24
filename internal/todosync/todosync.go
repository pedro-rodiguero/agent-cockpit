// Package todosync keeps the to-do list in step with a cockpit-todo-server: it sends every task the
// computer has, tombstones included, and merges back what the server answers (docs/todo-server.md).
package todosync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
)

// Every is how often the list syncs at most.
const Every = 30 * time.Second

// Timeout bounds one sync, so a slow server never holds a collection pass for long.
const Timeout = 3 * time.Second

// Link is where the list syncs: written by `cockpit todo connect`, in sync.json next to the list, readable
// only by its owner because it holds the device token.
type Link struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// Status is the outcome of the last sync, in sync-state.json.
type Status struct {
	At    time.Time `json:"at"`
	Error string    `json:"error,omitempty"`
}

// Paths are the files of a list's sync, next to the list.
type Paths struct{ Dir string }

func (p Paths) link() string  { return filepath.Join(p.Dir, "sync.json") }
func (p Paths) state() string { return filepath.Join(p.Dir, "sync-state.json") }

// For is the sync files of the list at listPath.
func For(listPath string) Paths { return Paths{Dir: filepath.Dir(listPath)} }

// CheckURL accepts the server's address: https, or http on this machine for testing.
func CheckURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("%q is not a server address like https://todo.example.com", raw)
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", errors.New("the server must use https")
	}
	return u.Scheme + "://" + u.Host, nil
}

// LoadLink reads the link; ok is false when the list is not connected.
func (p Paths) LoadLink() (Link, bool, error) {
	var l Link
	b, err := os.ReadFile(p.link())
	if errors.Is(err, fs.ErrNotExist) {
		return l, false, nil
	}
	if err != nil {
		return l, false, err
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return l, false, fmt.Errorf("%s: %w", p.link(), err)
	}
	return l, l.URL != "" && l.Token != "", nil
}

// SaveLink writes the link, readable only by its owner.
func (p Paths) SaveLink(l Link) error {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(l, "", "  ")
	tmp := p.link() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.link())
}

// Disconnect removes the link and the status.
func (p Paths) Disconnect() error {
	for _, f := range []string{p.link(), p.state()} {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// LoadStatus reads the outcome of the last sync.
func (p Paths) LoadStatus() Status {
	var s Status
	if b, err := os.ReadFile(p.state()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// SyncIfDue syncs store when it is connected and the last sync is at least Every old. Processes take
// turns: when another one is syncing, it returns at once. Errors are recorded in the status, which
// `cockpit todo sync` shows; the list itself keeps working offline.
func (p Paths) SyncIfDue(ctx context.Context, store *todo.Store, now time.Time) {
	link, ok, err := p.LoadLink()
	if err != nil || !ok || now.Sub(p.LoadStatus().At) < Every {
		return
	}
	unlock, ok := p.tryLock()
	if !ok {
		return
	}
	defer unlock()
	if now.Sub(p.LoadStatus().At) < Every { // another process synced while this one waited
		return
	}
	p.record(now, Sync(ctx, link, store, now))
}

// SyncNow syncs at once and records the outcome.
func (p Paths) SyncNow(ctx context.Context, store *todo.Store, now time.Time) error {
	link, ok, err := p.LoadLink()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("this computer is not connected: run cockpit todo connect <server>")
	}
	err = Sync(ctx, link, store, now)
	p.record(now, err)
	return err
}

func (p Paths) record(now time.Time, err error) {
	s := Status{At: now}
	if err != nil {
		s.Error = err.Error()
	}
	b, _ := json.Marshal(s)
	_ = os.WriteFile(p.state(), b, 0o600)
}

func (p Paths) tryLock() (unlock func(), ok bool) {
	f, err := os.OpenFile(p.state()+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, false
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, true
}

// client never follows a redirect: the token goes to the configured server and nowhere else.
var client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// Sync sends the list to the server and merges its answer back.
func Sync(ctx context.Context, link Link, store *todo.Store, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	l, err := store.Load()
	if err != nil {
		return err
	}
	tasks := l.Tasks
	if tasks == nil {
		tasks = []todo.Task{}
	}
	body, _ := json.Marshal(map[string][]todo.Task{"tasks": tasks})
	req, err := http.NewRequestWithContext(ctx, "POST", link.URL+"/api/sync", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+link.Token)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("server unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	var out struct {
		Tasks []todo.Task `json:"tasks"`
		Error string      `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != 200 {
		msg := out.Error
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("server said %d: %s", resp.StatusCode, msg)
	}
	return store.Update(now, func(l *todo.List) { l.Merge(out.Tasks) })
}
