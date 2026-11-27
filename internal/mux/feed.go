package mux

import "sync"

// maxQueued is how much typing a feed holds for a program that is not reading before it lets go of more.
const maxQueued = 4 << 20

// feed hands what is meant for a pane's terminal (typing, pastes, the emulator's answers to the
// program's questions, resizes) to it in order, from one goroutine, so that nothing that sends to a pane
// ever waits for the terminal.
//
// Waiting is what must not happen. A terminal takes input only as fast as its program and its console
// allow, and on Windows the console stops taking input while it is blocked printing. The server reads
// what the program prints, and to do that it needs the emulator's answers taken off its hands; were the
// answers written straight to the terminal, a full input pipe would stop the reading, which is what the
// console was waiting for: neither side moves again. And the goroutine that serves a dashboard would
// wait with them, so the dashboard's next message would never be taken and its whole window would freeze.
type feed struct {
	mu     sync.Mutex
	wake   *sync.Cond
	ops    []feedOp
	queued int
	closed bool
}

// feedOp is bytes to write, or, when data is nil, a new size.
type feedOp struct {
	data       []byte
	cols, rows int
}

func newFeed() *feed {
	f := &feed{}
	f.wake = sync.NewCond(&f.mu)
	return f
}

// Write queues b for the terminal. It never blocks and never fails: typing beyond maxQueued for a
// program that reads none of it is dropped.
func (f *feed) Write(b []byte) (int, error) {
	f.mu.Lock()
	if !f.closed && f.queued+len(b) <= maxQueued {
		f.ops = append(f.ops, feedOp{data: append([]byte(nil), b...)})
		f.queued += len(b)
		f.wake.Signal()
	}
	f.mu.Unlock()
	return len(b), nil
}

// resize queues a new size, after whatever was typed before it.
func (f *feed) resize(cols, rows int) {
	f.mu.Lock()
	if !f.closed {
		f.ops = append(f.ops, feedOp{cols: cols, rows: rows})
		f.wake.Signal()
	}
	f.mu.Unlock()
}

// close ends run, leaving what is still queued unsent.
func (f *feed) close() {
	f.mu.Lock()
	f.closed = true
	f.ops = nil
	f.wake.Signal()
	f.mu.Unlock()
}

// run sends the queue to t until close; it is the only goroutine that writes to or resizes t.
func (f *feed) run(t terminal) {
	for {
		f.mu.Lock()
		for len(f.ops) == 0 && !f.closed {
			f.wake.Wait()
		}
		if f.closed {
			f.mu.Unlock()
			return
		}
		op := f.ops[0]
		f.ops = f.ops[1:]
		f.queued -= len(op.data)
		f.mu.Unlock()
		if op.data == nil {
			_ = t.Resize(op.cols, op.rows)
		} else if _, err := t.Write(op.data); err != nil {
			f.close()
			return
		}
	}
}
