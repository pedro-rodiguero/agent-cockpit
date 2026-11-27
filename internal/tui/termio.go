package tui

import (
	"bufio"
	"fmt"
	"os"
	"sync"

	"golang.org/x/term"
)

// Terminal setup and teardown sequences.
const (
	// alternate screen, hide the cursor, enable bracketed paste, report mouse presses and drags (SGR
	// encoding), clear
	enterSeq = "\x1b[?1049h\x1b[?25l\x1b[?2004h\x1b[?1002h\x1b[?1006h\x1b[2J"
	// reset attributes, clear, stop mouse reports, disable bracketed paste, show the cursor, leave the
	// alternate screen
	leaveSeq = "\x1b[0m\x1b[2J\x1b[?1006l\x1b[?1002l\x1b[?2004l\x1b[?25h\x1b[?1049l"

	syncBegin = "\x1b[?2026h" // synchronized update: the terminal shows the frame only when it is complete
	syncEnd   = "\x1b[?2026l"

	// The cursor's shape (DECSCUSR): a block that stays lit, one that blinks, and the terminal's own.
	cursorSteady  = "\x1b[2 q"
	cursorBlink   = "\x1b[1 q"
	cursorDefault = "\x1b[0 q"

	frameBufSize = 256 << 10 // one full frame of a large window fits without intermediate flushes
)

// ownTermProgram is the TERM_PROGRAM the menu bar app gives its dashboard window.
const ownTermProgram = "agent-cockpit"

// fallbackCols and fallbackRows are the window size assumed when the terminal does not report one (for
// instance when stdout is redirected), so the dashboard still renders something readable.
const (
	fallbackCols = 120
	fallbackRows = 40
)

// screen owns the real terminal: raw mode, alternate screen, and line-diffed frames.
type screen struct {
	in, out *os.File
	restore *term.State
	w       *bufio.Writer
	prev    []string
	pw, ph  int
	pcur    cursor       // the cursor the previous frame left
	down    *downsampler // set when the terminal cannot show 24-bit color
	// ownWindow is the menu bar app's dashboard window. Its cursor blinks on a clock of its own that
	// typing does not restart, so a cursor that is being typed at spends half the time unlit: there the
	// dashboard holds the cursor lit while the person acts and lets it blink again afterwards.
	ownWindow bool
	unprepare func() // undoes what the console needed to show the dashboard; nil when nothing did
	closeOnce sync.Once
}

func openScreen() (*screen, error) {
	s := &screen{in: os.Stdin, out: os.Stdout, ownWindow: os.Getenv("TERM_PROGRAM") == ownTermProgram}
	st, err := term.MakeRaw(int(s.in.Fd()))
	if err != nil {
		return nil, err
	}
	s.restore = st
	if detectColorDepth(os.Getenv) == color256 {
		s.down = newDownsampler()
	}
	s.unprepare = prepareConsole(s.in, s.out)
	s.w = bufio.NewWriterSize(s.out, frameBufSize)
	s.w.WriteString(enterSeq)
	s.w.Flush()
	return s, nil
}

// close restores the terminal. It is safe to call more than once and from any goroutine: it writes
// straight to the terminal rather than through the frame buffer, so it also works while crashing in the
// middle of a frame.
func (s *screen) close() {
	s.closeOnce.Do(func() {
		if s.ownWindow {
			_, _ = s.out.WriteString(cursorDefault)
		}
		_, _ = s.out.WriteString(leaveSeq)
		_ = term.Restore(int(s.in.Fd()), s.restore)
		if s.unprepare != nil {
			s.unprepare()
		}
	})
}

// size returns the window size in cells, or the fallback size when the terminal does not report one.
func (s *screen) size() (int, int) {
	w, h, err := term.GetSize(int(s.out.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return fallbackCols, fallbackRows
	}
	return w, h
}

type cursor struct {
	x, y   int
	show   bool
	steady bool // lit without blinking, while the person types
}

// repaint makes the next frame redraw every line, after a change of theme.
func (s *screen) repaint() {
	s.prev = nil
	if s.down != nil {
		s.down = newDownsampler()
	}
}

// frame draws lines, rewriting only the ones that changed since the previous frame. A frame that
// changes nothing writes nothing: the terminal would otherwise redraw for its sync and cursor sequences.
func (s *screen) frame(lines []string, w, h int, cur cursor) {
	if w != s.pw || h != s.ph {
		s.prev = nil
		s.pw, s.ph = w, h
		s.w.WriteString("\x1b[0m\x1b[2J")
	} else if cur == s.pcur && sameLines(s.prev, lines, h) {
		return
	}
	s.pcur = cur
	s.w.WriteString(syncBegin + "\x1b[?25l")
	for i, l := range lines {
		if i >= h {
			break
		}
		if i < len(s.prev) && s.prev[i] == l {
			continue
		}
		out := l
		if s.down != nil {
			out = s.down.line(l)
		}
		fmt.Fprintf(s.w, "\x1b[%d;1H%s\x1b[0m", i+1, out)
	}
	if cur.show {
		if s.ownWindow && cur.steady {
			s.w.WriteString(cursorSteady)
		} else if s.ownWindow {
			s.w.WriteString(cursorBlink)
		}
		fmt.Fprintf(s.w, "\x1b[%d;%dH\x1b[?25h", cur.y+1, cur.x+1)
	}
	s.w.WriteString(syncEnd)
	s.w.Flush()
	s.prev = append(s.prev[:0], lines...)
}

// sameLines reports whether the first h of lines are what prev already shows.
func sameLines(prev, lines []string, h int) bool {
	n := min(len(lines), h)
	if len(prev) != n {
		return false
	}
	for i := 0; i < n; i++ {
		if prev[i] != lines[i] {
			return false
		}
	}
	return true
}
