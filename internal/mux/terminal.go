package mux

import "io"

// terminal is a program running on a pseudo-terminal: what it prints is read, what is typed is written.
// Unix gives one through a pty, Windows through ConPTY.
type terminal interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	PID() int
	// Wait returns the program's exit code once it has ended.
	Wait() int
	// Hangup asks the program to leave, the way closing its terminal window would.
	Hangup()
	// Kill ends the program without asking.
	Kill()
}
