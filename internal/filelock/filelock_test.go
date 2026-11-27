package filelock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTryLockSeesAHeldLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	open := func() *os.File {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	a, b := open(), open()
	if err := Lock(a); err != nil {
		t.Fatal(err)
	}
	if ok, err := TryLock(b); ok || err != nil {
		t.Fatalf("took a lock that is held: %v, %v", ok, err)
	}
	if err := Unlock(a); err != nil {
		t.Fatal(err)
	}
	if ok, err := TryLock(b); !ok || err != nil {
		t.Fatalf("a free lock was refused: %v, %v", ok, err)
	}
}
