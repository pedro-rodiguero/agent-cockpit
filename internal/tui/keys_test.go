package tui

import (
	"reflect"
	"testing"
)

// feedAll feeds each chunk as one read and returns every key decoded, without flushing.
func feedAll(d *keyDecoder, chunks ...string) []key {
	var keys []key
	for _, c := range chunks {
		keys = append(keys, d.feed([]byte(c))...)
	}
	return keys
}

func TestDecodeSplitCSI(t *testing.T) {
	var d keyDecoder
	if keys := d.feed([]byte("\x1b[")); len(keys) != 0 {
		t.Fatalf("incomplete CSI decoded early: %+v", keys)
	}
	keys := d.feed([]byte("Aj"))
	want := []key{{code: keyUp, raw: []byte("\x1b[A")}, {code: keyRune, text: "j", raw: []byte("j")}}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("got %+v, want %+v", keys, want)
	}
	keys = feedAll(&d, "\x1b[5", "~")
	if len(keys) != 1 || keys[0].code != keyPgUp {
		t.Fatalf("split pgup: %+v", keys)
	}
}

func TestDecodePasteAcrossReads(t *testing.T) {
	var d keyDecoder
	chunks := []string{"\x1b[200~hap", "py p", "y\x1b[20"}
	for _, c := range chunks {
		if keys := d.feed([]byte(c)); len(keys) != 0 {
			t.Fatalf("read %q produced keys before the paste ended: %+v", c, keys)
		}
	}
	keys := d.feed([]byte("1~q"))
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want paste and q: %+v", len(keys), keys)
	}
	if keys[0].code != keyPaste || keys[0].text != "happy py" {
		t.Fatalf("paste = %+v", keys[0])
	}
	if !keys[1].isRune("q") {
		t.Fatalf("after paste = %+v", keys[1])
	}
	if d.inPaste || d.pendingEscape() {
		t.Fatal("decoder left in a paste")
	}
}

func TestDecodePasteNotEndedByTimeout(t *testing.T) {
	var d keyDecoder
	d.feed([]byte("\x1b[200~abc"))
	if keys := d.flush(); len(keys) != 0 {
		t.Fatalf("flush ended a paste: %+v", keys)
	}
	keys := d.feed([]byte("\x1b[201~"))
	if len(keys) != 1 || keys[0].text != "abc" {
		t.Fatalf("got %+v", keys)
	}
}

func TestDecodeSplitUTF8(t *testing.T) {
	var d keyDecoder
	if keys := d.feed([]byte{0xc3}); len(keys) != 0 {
		t.Fatalf("half a character decoded: %+v", keys)
	}
	keys := d.feed([]byte{0xa9})
	if len(keys) != 1 || !keys[0].isRune("é") {
		t.Fatalf("got %+v", keys)
	}
}

func TestDecodeLoneEscResolvedByFlush(t *testing.T) {
	var d keyDecoder
	if keys := d.feed([]byte("\x1b")); len(keys) != 0 {
		t.Fatalf("lone ESC decoded before the timeout: %+v", keys)
	}
	if !d.pendingEscape() {
		t.Fatal("lone ESC not pending")
	}
	keys := d.flush()
	if len(keys) != 1 || keys[0].code != keyEsc || string(keys[0].raw) != "\x1b" {
		t.Fatalf("flush = %+v", keys)
	}
	if d.pendingEscape() || d.flush() != nil {
		t.Fatal("flush left something pending")
	}
}

func TestDecodeIncompleteSequenceFlushedVerbatim(t *testing.T) {
	var d keyDecoder
	d.feed([]byte("\x1b[1;5"))
	keys := d.flush()
	if len(keys) != 1 || keys[0].code != keyUnknown || string(keys[0].raw) != "\x1b[1;5" {
		t.Fatalf("flush = %+v", keys)
	}
}

func TestDecodeEscEsc(t *testing.T) {
	var d keyDecoder
	keys := d.feed([]byte("\x1b\x1b"))
	if len(keys) != 1 || keys[0].code != keyEsc {
		t.Fatalf("feed = %+v", keys)
	}
	keys = d.flush()
	if len(keys) != 1 || keys[0].code != keyEsc {
		t.Fatalf("flush = %+v", keys)
	}
}

func TestDecodeControlKeys(t *testing.T) {
	var d keyDecoder
	keys := d.feed([]byte("\x03\x11\r\t\x7f\x00"))
	want := []struct {
		code keyCode
		text string
	}{{keyCtrl, "c"}, {keyCtrl, "q"}, {keyEnter, ""}, {keyTab, ""}, {keyBackspace, ""}, {keyCtrl, "@"}}
	if len(keys) != len(want) {
		t.Fatalf("got %d keys: %+v", len(keys), keys)
	}
	for i, w := range want {
		if keys[i].code != w.code || keys[i].text != w.text {
			t.Errorf("key %d = %+v, want %v %q", i, keys[i], w.code, w.text)
		}
	}
}

func TestDecodeAltMultibyte(t *testing.T) {
	var d keyDecoder
	keys := d.feed([]byte("\x1bé"))
	if len(keys) != 1 || keys[0].text != "é" || !keys[0].alt || string(keys[0].raw) != "\x1bé" {
		t.Fatalf("got %+v", keys)
	}
	keys = feedAll(&d, "\x1b\xc3", "\xa9")
	if len(keys) != 1 || keys[0].text != "é" || !keys[0].alt {
		t.Fatalf("split alt+é: %+v", keys)
	}
}

func TestDecodeSS3(t *testing.T) {
	var d keyDecoder
	keys := feedAll(&d, "\x1bO", "B")
	if len(keys) != 1 || keys[0].code != keyDown {
		t.Fatalf("got %+v", keys)
	}
}
