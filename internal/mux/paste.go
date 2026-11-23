package mux

import "bytes"

var (
	bracketedPasteOn  = []byte("\x1b[?2004h")
	bracketedPasteOff = []byte("\x1b[?2004l")
	pasteStart        = []byte("\x1b[200~")
	pasteEnd          = []byte("\x1b[201~")
)

// pasteMode follows whether the program in a pane has enabled bracketed paste (DECSET 2004), by
// watching its output. The emulator tracks the same mode but offers no way to read it without racing
// its writer, and a paste must be bracketed only for programs that asked: a plain shell or REPL would
// otherwise receive the markers as typed text.
type pasteMode struct {
	on   bool
	tail []byte // end of the previous chunk, in case a sequence is split across reads
}

// observe scans one chunk of program output.
func (m *pasteMode) observe(chunk []byte) {
	buf := append(m.tail, chunk...)
	on, off := bytes.LastIndex(buf, bracketedPasteOn), bytes.LastIndex(buf, bracketedPasteOff)
	if on > off {
		m.on = true
	} else if off > on {
		m.on = false
	}
	keep := len(bracketedPasteOn) - 1
	if len(buf) > keep {
		buf = buf[len(buf)-keep:]
	}
	m.tail = append(m.tail[:0], buf...)
}

// wrap returns text as the program should receive a paste of it. Any end marker inside the text is
// removed so pasted content cannot terminate the paste early.
func (m *pasteMode) wrap(text []byte) []byte {
	if !m.on {
		return text
	}
	text = bytes.ReplaceAll(text, pasteEnd, nil)
	out := make([]byte, 0, len(text)+len(pasteStart)+len(pasteEnd))
	out = append(out, pasteStart...)
	out = append(out, text...)
	return append(out, pasteEnd...)
}
