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
	// alternate screen, hide the cursor, enable bracketed paste, clear
	enterSeq = "\x1b[?1049h\x1b[?25l\x1b[?2004h\x1b[2J"
	// reset attributes, clear, disable bracketed paste, show the cursor, leave the alternate screen
	leaveSeq = "\x1b[0m\x1b[2J\x1b[?2004l\x1b[?25h\x1b[?1049l"

	syncBegin = "\x1b[?2026h" // synchronized update: the terminal shows the frame only when it is complete
	syncEnd   = "\x1b[?2026l"

	frameBufSize = 256 << 10 // one full frame of a large window fits without intermediate flushes
)

// fallbackCols and fallbackRows are the window size assumed when the terminal does not report one (for
// instance when stdout is redirected), so the dashboard still renders something readable.
const (
	fallbackCols = 120
	fallbackRows = 40
)

// screen owns the real terminal: raw mode, alternate screen, and line-diffed frames.
type screen struct {
	in, out   *os.File
	restore   *term.State
	w         *bufio.Writer
	prev      []string
	pw, ph    int
	down      *downsampler // set when the terminal cannot show 24-bit color
	closeOnce sync.Once
}

func openScreen() (*screen, error) {
	s := &screen{in: os.Stdin, out: os.Stdout}
	st, err := term.MakeRaw(int(s.in.Fd()))
	if err != nil {
		return nil, err
	}
	s.restore = st
	if detectColorDepth(os.Getenv) == color256 {
		s.down = newDownsampler()
	}
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
		_, _ = s.out.WriteString(leaveSeq)
		_ = term.Restore(int(s.in.Fd()), s.restore)
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
	x, y int
	show bool
}

// repaint makes the next frame redraw every line, after a change of theme.
func (s *screen) repaint() {
	s.prev = nil
	if s.down != nil {
		s.down = newDownsampler()
	}
}

// frame draws lines, rewriting only the ones that changed since the previous frame.
func (s *screen) frame(lines []string, w, h int, cur cursor) {
	if w != s.pw || h != s.ph {
		s.prev = nil
		s.pw, s.ph = w, h
		s.w.WriteString("\x1b[0m\x1b[2J")
	}
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
		fmt.Fprintf(s.w, "\x1b[%d;%dH\x1b[?25h", cur.y+1, cur.x+1)
	}
	s.w.WriteString(syncEnd)
	s.w.Flush()
	s.prev = append(s.prev[:0], lines...)
}
