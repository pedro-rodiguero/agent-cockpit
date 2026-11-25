package mux

import "bytes"

const (
	// maxOSC is the longest OSC sequence held back whole; a longer one (a big OSC 52 clipboard write)
	// is passed on as it is.
	maxOSC  = 1 << 20
	escByte = 0x1b
)

// oscFilter takes OSC sequences ("ESC ] ... BEL" or "ESC ] ... ESC \") out of a program's output
// before the emulator sees them, when they carry non-ASCII text. The emulator ends an OSC string at
// any 0x9C byte (8-bit ST), and 0x9C is also a UTF-8 continuation byte: the title "✳ Claude Code"
// (E2 9C B3 ...) ended after its first byte, and the rest of it was printed on the screen at the
// cursor. Titles (OSC 0 and 2) are handed to title; other non-ASCII OSC sequences are dropped, which
// at worst loses a hyperlink target. ASCII-only ones go through untouched.
type oscFilter struct {
	held  []byte // an OSC sequence, or an ESC that may start one, not yet complete
	title func(string)
}

// feed returns what of b should reach the emulator; an incomplete sequence at its end is held back.
func (f *oscFilter) feed(b []byte) []byte {
	if len(f.held) == 0 && bytes.IndexByte(b, escByte) < 0 {
		return b
	}
	in := append(f.held, b...)
	f.held = nil
	out := make([]byte, 0, len(in))
	for len(in) > 0 {
		i := bytes.IndexByte(in, escByte)
		if i < 0 {
			out = append(out, in...)
			break
		}
		out = append(out, in[:i]...)
		in = in[i:]
		if len(in) == 1 {
			f.held = append(f.held, in...)
			break
		}
		if in[1] != ']' {
			out = append(out, in[:2]...)
			in = in[2:]
			continue
		}
		end, n := oscEnd(in)
		if end < 0 {
			if len(in) > maxOSC {
				out = append(out, in...)
			} else {
				f.held = append(f.held, in...)
			}
			break
		}
		out = append(out, f.osc(in[:n], in[2:end])...)
		in = in[n:]
	}
	return out
}

// oscEnd finds the end of the OSC sequence at the start of s: where its payload ends and the length of
// the whole sequence, or -1 while it is incomplete.
func oscEnd(s []byte) (end, n int) {
	for i := 2; i < len(s); i++ {
		switch s[i] {
		case '\a':
			return i, i + 1
		case escByte:
			if i+1 == len(s) {
				return -1, 0
			}
			if s[i+1] == '\\' {
				return i, i + 2
			}
		}
	}
	return -1, 0
}

// osc decides what a complete OSC sequence becomes.
func (f *oscFilter) osc(seq, payload []byte) []byte {
	ascii := true
	for _, c := range payload {
		if c >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return seq
	}
	if cmd, text, ok := bytes.Cut(payload, []byte(";")); ok && (string(cmd) == "0" || string(cmd) == "2") && f.title != nil {
		f.title(string(text))
	}
	return nil
}
