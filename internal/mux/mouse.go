package mux

import (
	"fmt"
	"strconv"
	"strings"
)

// mouseMode follows whether the program in a pane has asked for mouse reports (DECSET 9, 1000, 1002
// or 1003) and in which encoding (1006 for SGR), by watching its output, for the same reason pasteMode
// does: the emulator keeps its modes to itself.
type mouseMode struct {
	tracking map[int]bool
	sgr      bool
	tail     []byte // end of the previous chunk, in case a sequence is split across reads
}

// maxModeSeq is the longest "ESC [ ? params h" kept across reads.
const maxModeSeq = 32

// observe scans one chunk of program output. Sequences in the kept tail were seen already; seeing
// them again before the new chunk's own leaves the same final state.
func (m *mouseMode) observe(chunk []byte) {
	buf := append(m.tail, chunk...)
	s := string(buf)
	for i := strings.Index(s, "\x1b[?"); i >= 0; {
		rest := s[i+3:]
		end := strings.IndexAny(rest, "hl")
		if end >= 0 && strings.Trim(rest[:end], "0123456789;") == "" {
			for _, p := range strings.Split(rest[:end], ";") {
				n, err := strconv.Atoi(p)
				if err == nil {
					m.set(n, rest[end] == 'h')
				}
			}
		}
		next := strings.Index(rest, "\x1b[?")
		if next < 0 {
			break
		}
		i += 3 + next
	}
	if strings.Contains(s, "\x1bc") { // full reset
		m.tracking, m.sgr = nil, false
	}
	if len(buf) > maxModeSeq {
		buf = buf[len(buf)-maxModeSeq:]
	}
	m.tail = append(m.tail[:0], buf...)
}

func (m *mouseMode) set(mode int, on bool) {
	switch mode {
	case 9, 1000, 1002, 1003:
		if m.tracking == nil {
			m.tracking = map[int]bool{}
		}
		if on {
			m.tracking[mode] = true
		} else {
			delete(m.tracking, mode)
		}
	case 1006:
		m.sgr = on
	}
}

// wheelArrows is how many cursor keys a wheel notch becomes for a full-screen program without mouse
// reports.
const wheelArrows = 3

// wheel is what one notch of the wheel at column x, row y of the pane (from 0) sends the program: a
// mouse report if it asked for them, cursor keys if it is full-screen without them, as terminals do,
// and nothing otherwise.
func (m *mouseMode) wheel(up bool, x, y int, alt bool) []byte {
	btn := 65
	if up {
		btn = 64
	}
	switch {
	case len(m.tracking) > 0 && m.sgr:
		return []byte(fmt.Sprintf("\x1b[<%d;%d;%dM", btn, x+1, y+1))
	case len(m.tracking) > 0:
		return []byte{0x1b, '[', 'M', byte(32 + btn), byte(min(223, 33+x)), byte(min(223, 33+y))}
	case alt:
		arrow := "\x1b[B"
		if up {
			arrow = "\x1b[A"
		}
		return []byte(strings.Repeat(arrow, wheelArrows))
	}
	return nil
}
