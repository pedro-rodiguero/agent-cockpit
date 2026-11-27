package mux

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

// slowTerminal takes nothing until released, like a console that is busy printing.
type slowTerminal struct {
	release chan struct{}
	mu      sync.Mutex
	got     bytes.Buffer
	sizes   [][2]int
}

func (t *slowTerminal) Read([]byte) (int, error) { select {} }
func (t *slowTerminal) Write(b []byte) (int, error) {
	<-t.release
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.got.Write(b)
}
func (t *slowTerminal) Close() error { return nil }
func (t *slowTerminal) Resize(cols, rows int) error {
	<-t.release
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sizes = append(t.sizes, [2]int{cols, rows})
	return nil
}
func (t *slowTerminal) PID() int  { return 0 }
func (t *slowTerminal) Wait() int { return 0 }
func (t *slowTerminal) Hangup()   {}
func (t *slowTerminal) Kill()     {}

// Nothing that sends to a pane waits for a terminal that is not taking input, and what was sent arrives
// in order once it does.
func TestFeedNeverMakesItsSendersWait(t *testing.T) {
	term := &slowTerminal{release: make(chan struct{})}
	f := newFeed()
	go f.run(term)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			_, _ = f.Write([]byte("ab"))
		}
		f.resize(120, 40)
		_, _ = f.Write([]byte("!"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a sender waited for the terminal")
	}
	close(term.release)
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		term.mu.Lock()
		n, sizes := term.got.Len(), len(term.sizes)
		term.mu.Unlock()
		if n == 2001 && sizes == 1 {
			break
		}
	}
	term.mu.Lock()
	defer term.mu.Unlock()
	if got := term.got.String(); len(got) != 2001 || got[len(got)-1] != '!' || len(term.sizes) != 1 || term.sizes[0] != [2]int{120, 40} {
		t.Fatalf("%d bytes arrived, sizes %v", len(got), term.sizes)
	}

	// A program that reads nothing does not fill the memory: beyond the limit, typing is dropped.
	stuck := newFeed()
	big := make([]byte, 1<<20)
	for i := 0; i < 10; i++ {
		_, _ = stuck.Write(big)
	}
	if stuck.queued > maxQueued {
		t.Fatalf("%d bytes queued", stuck.queued)
	}
	f.close()
}
