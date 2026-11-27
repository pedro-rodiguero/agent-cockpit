package mux

import (
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"
)

// Switching between panes resizes them while their shells redraw and ask the terminal questions. The
// server must keep taking a dashboard's messages through all of it: when it stops, the dashboard's next
// write never returns and the whole window freezes.
func TestServerKeepsUpWithResizesAndTypingWhileShellsRedraw(t *testing.T) {
	s := newServer()
	var ids []string
	for i := 0; i < 2; i++ {
		id, err := s.spawn(Msg{Agent: "shell", Cwd: t.TempDir(), Cols: 80, Rows: 24})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		defer s.close(id)
	}
	time.Sleep(3 * time.Second) // the shells start and draw their prompts

	conn, end := net.Pipe()
	go s.handle(end)
	go func() { _, _ = io.Copy(io.Discard, conn) }()
	defer conn.Close()

	done := make(chan int, 1)
	go func() {
		enc := json.NewEncoder(conn)
		sent := 0
		for round := 0; round < 150; round++ {
			for i, id := range ids {
				cols, rows := 80, 24
				if (round+i)%2 == 0 {
					cols, rows = 160, 50
				}
				msgs := []Msg{
					{Op: OpResize, ID: id, Cols: cols, Rows: rows},
					{Op: OpInput, ID: id, Data: []byte("\x1b[I")}, // the pane got the keyboard
					{Op: OpInput, ID: id, Data: []byte("Get-ChildItem Env: | Out-String\r")},
					{Op: OpInput, ID: id, Data: []byte("\x1b[O")},
				}
				for _, m := range msgs {
					if enc.Encode(m) != nil {
						done <- sent
						return
					}
					sent++
				}
			}
		}
		done <- sent
	}()
	select {
	case n := <-done:
		if n != 150*2*4 {
			t.Fatalf("only %d messages went through", n)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the server stopped taking messages: a dashboard would be frozen")
	}
}
