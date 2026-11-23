package tui

import (
	"maps"
	"sync"

	"github.com/pedro-rodiguero/agent-cockpit/internal/term"
)

// wantedLabels is the tab title each session in a terminal tab should have (tty → title).
func wantedLabels(tiles []tile) map[string]string {
	want := map[string]string{}
	for _, t := range tiles {
		if t.pane != nil || t.sess == nil || t.sess.TTY == "" {
			continue
		}
		want[t.sess.TTY] = look(t.state()).glyph + " " + t.sess.Agent + " · " + t.name()
	}
	return want
}

// labelDiff returns the titles to set to go from the titles shown to the ones wanted (an empty title
// clears a tab that no longer has a session), and the titles shown afterwards. Clearing every label is
// labelDiff(shown, nil).
func labelDiff(shown, want map[string]string) (changes, next map[string]string) {
	changes, next = map[string]string{}, map[string]string{}
	for tty, title := range want {
		next[tty] = title
		if shown[tty] != title {
			changes[tty] = title
		}
	}
	for tty := range shown {
		if _, ok := want[tty]; !ok {
			changes[tty] = ""
		}
	}
	return changes, next
}

// labeler applies tab titles on one goroutine, in order. AppleScript takes a while, so changes that
// arrive while it runs are merged and applied together afterwards, the newest title per tab winning.
type labeler struct {
	mu      sync.Mutex
	queued  map[string]string
	wake    chan struct{}
	stop    chan struct{}
	stopped chan struct{}
	onErr   func(error)
}

func startLabeler(onErr func(error)) *labeler {
	l := &labeler{wake: make(chan struct{}, 1), stop: make(chan struct{}), stopped: make(chan struct{}), onErr: onErr}
	go l.run()
	return l
}

// set queues title changes (tty → title, "" clears).
func (l *labeler) set(changes map[string]string) {
	if len(changes) == 0 {
		return
	}
	l.mu.Lock()
	if l.queued == nil {
		l.queued = map[string]string{}
	}
	maps.Copy(l.queued, changes)
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// close applies what is still queued and waits for the worker to finish.
func (l *labeler) close() {
	close(l.stop)
	<-l.stopped
}

func (l *labeler) run() {
	defer close(l.stopped)
	for {
		select {
		case <-l.wake:
			l.apply()
		case <-l.stop:
			l.apply()
			return
		}
	}
}

func (l *labeler) apply() {
	l.mu.Lock()
	batch := l.queued
	l.queued = nil
	l.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if err := term.Label(batch); err != nil {
		l.onErr(err)
	}
}
