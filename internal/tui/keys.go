package tui

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// keyCode identifies a key. Printable characters are keyRune, with the character in key.text.
type keyCode int

const (
	keyUnknown keyCode = iota // an escape sequence the dashboard has no name for
	keyRune
	keyCtrl // a control character; key.text holds its letter, "c" for ctrl-c
	keyPaste
	keyEnter
	keyTab
	keyShiftTab
	keyBackspace
	keyDelete
	keyEsc
	keyUp
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyPgUp
	keyPgDown
	keyMouse // a mouse report; key.mouse holds it
)

// key is one decoded key press or paste.
type key struct {
	code  keyCode
	text  string // the character (keyRune), the letter (keyCtrl), or the pasted text (keyPaste)
	alt   bool   // pressed with alt/option, which terminals send as an ESC prefix
	raw   []byte // the bytes that encoded it, forwarded verbatim to a pane; nil for keyPaste
	mouse mouseEvent
}

const (
	escByte = 0x1b
	delByte = 0x7f
)

var (
	pasteStart = []byte("\x1b[200~")
	pasteEnd   = []byte("\x1b[201~")
)

// csiKeys names the CSI and SS3 sequences the dashboard reacts to, by everything after "ESC [" or "ESC O".
var csiKeys = map[string]keyCode{
	"A": keyUp, "B": keyDown, "C": keyRight, "D": keyLeft,
	"H": keyHome, "1~": keyHome, "F": keyEnd, "4~": keyEnd,
	"5~": keyPgUp, "6~": keyPgDown, "3~": keyDelete, "Z": keyShiftTab,
}

// keyDecoder turns terminal input into keys. Reads can end in the middle of an escape sequence, a UTF-8
// character or a bracketed paste, so whatever is incomplete is kept for the next read. A lone ESC is
// ambiguous (the Esc key, or the start of a sequence whose rest is still in flight); it stays pending
// until the caller decides no more input is coming and calls flush.
type keyDecoder struct {
	pending []byte // an incomplete sequence or character, decoded once more bytes arrive
	inPaste bool   // between the paste start and end markers
	paste   []byte // pasted text received so far
}

// feed decodes b after whatever was pending and returns the keys it completes.
func (d *keyDecoder) feed(b []byte) []key {
	buf := append(d.pending, b...)
	d.pending = nil
	var keys []key
	for len(buf) > 0 {
		if d.inPaste {
			var done bool
			buf, done = d.feedPaste(buf)
			if !done {
				break
			}
			keys = append(keys, key{code: keyPaste, text: string(d.paste)})
			d.paste, d.inPaste = nil, false
			continue
		}
		if bytes.HasPrefix(buf, pasteStart) {
			d.inPaste = true
			buf = buf[len(pasteStart):]
			continue
		}
		k, n := decodeKey(buf)
		if n == 0 {
			d.pending = append([]byte(nil), buf...)
			break
		}
		keys = append(keys, k)
		buf = buf[n:]
	}
	return keys
}

// feedPaste consumes pasted bytes up to the end marker. It reports whether the paste ended, and returns
// the input after the marker. A partial end marker at the end of buf is kept pending.
func (d *keyDecoder) feedPaste(buf []byte) (rest []byte, done bool) {
	if i := bytes.Index(buf, pasteEnd); i >= 0 {
		d.paste = append(d.paste, buf[:i]...)
		return buf[i+len(pasteEnd):], true
	}
	keep := partialSuffix(buf, pasteEnd)
	d.paste = append(d.paste, buf[:len(buf)-keep]...)
	d.pending = append([]byte(nil), buf[len(buf)-keep:]...)
	return nil, false
}

// pendingEscape reports whether the decoder holds bytes outside a paste that only time can resolve.
func (d *keyDecoder) pendingEscape() bool { return !d.inPaste && len(d.pending) > 0 }

// flush resolves pending bytes once no more input followed them: a lone ESC is the Esc key, anything
// else is passed on unnamed so a pane still receives it. A paste in progress is left alone; it only ends
// at its end marker.
func (d *keyDecoder) flush() []key {
	if !d.pendingEscape() {
		return nil
	}
	raw := d.pending
	d.pending = nil
	if len(raw) == 1 && raw[0] == escByte {
		return []key{{code: keyEsc, raw: raw}}
	}
	return []key{{code: keyUnknown, raw: raw}}
}

// decodeKey decodes the key at the start of b and how many bytes it used; 0 means b ends before the key
// does.
func decodeKey(b []byte) (key, int) {
	if b[0] != escByte {
		return decodeChar(b)
	}
	if len(b) == 1 {
		return key{}, 0
	}
	switch b[1] {
	case '[':
		return decodeCSI(b)
	case 'O':
		if len(b) < 3 {
			return key{}, 0
		}
		return key{code: csiKeys[string(b[2])], raw: b[:3]}, 3
	case escByte:
		return key{code: keyEsc, raw: b[:1]}, 1
	}
	k, n := decodeChar(b[1:])
	if n == 0 {
		return key{}, 0
	}
	k.alt, k.raw = true, b[:n+1]
	return k, n + 1
}

// decodeCSI decodes "ESC [ parameters intermediates final".
func decodeCSI(b []byte) (key, int) {
	i := 2
	for i < len(b) && b[i] >= 0x20 && b[i] <= 0x3f {
		i++
	}
	if i == len(b) {
		return key{}, 0
	}
	if b[i] < 0x40 || b[i] > 0x7e {
		// Not a sequence after all: ESC followed by '[' typed quickly.
		return key{code: keyRune, text: "[", alt: true, raw: b[:2]}, 2
	}
	n := i + 1
	if m, ok := decodeMouse(b[2:i], b[i]); ok {
		return key{code: keyMouse, mouse: m}, n
	}
	return key{code: csiKeys[string(b[2:n])], raw: b[:n]}, n
}

// decodeChar decodes one control character or UTF-8 encoded character.
func decodeChar(b []byte) (key, int) {
	c := b[0]
	switch {
	case c == '\r' || c == '\n':
		return key{code: keyEnter, raw: b[:1]}, 1
	case c == '\t':
		return key{code: keyTab, raw: b[:1]}, 1
	case c == delByte || c == '\b':
		return key{code: keyBackspace, raw: b[:1]}, 1
	case c < 0x20:
		// Control characters are the letter's code minus 0x40: 0x01 is ctrl-a, 0x11 is ctrl-q.
		return key{code: keyCtrl, text: string(unicode.ToLower(rune('@' + c))), raw: b[:1]}, 1
	case !utf8.FullRune(b):
		return key{}, 0
	}
	r, n := utf8.DecodeRune(b)
	return key{code: keyRune, text: string(r), raw: b[:n]}, n
}

// partialSuffix returns the length of the longest proper prefix of marker that b ends with.
func partialSuffix(b, marker []byte) int {
	for n := min(len(b), len(marker)-1); n > 0; n-- {
		if bytes.HasSuffix(b, marker[:n]) {
			return n
		}
	}
	return 0
}

// isCtrl reports whether k is ctrl plus the given letter.
func (k key) isCtrl(letter string) bool { return k.code == keyCtrl && k.text == letter }

// isRune reports whether k is the plain character s, without alt.
func (k key) isRune(s string) bool { return k.code == keyRune && !k.alt && k.text == s }

// decodeMouse decodes the parameters and final byte of an SGR mouse report, "ESC [ < b ; x ; y M"
// (press or motion) or "... m" (release).
func decodeMouse(params []byte, final byte) (mouseEvent, bool) {
	if len(params) == 0 || params[0] != '<' || (final != 'M' && final != 'm') {
		return mouseEvent{}, false
	}
	f := strings.Split(string(params[1:]), ";")
	if len(f) != 3 {
		return mouseEvent{}, false
	}
	var v [3]int
	for i, s := range f {
		n, err := strconv.Atoi(s)
		if err != nil {
			return mouseEvent{}, false
		}
		v[i] = n
	}
	// Shift, alt and ctrl add 4, 8 and 16 to the button; they change nothing here.
	return mouseEvent{btn: v[0] &^ 28, x: v[1] - 1, y: v[2] - 1, release: final == 'm'}, true
}
