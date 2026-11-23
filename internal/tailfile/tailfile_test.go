package tailfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte("first line\nsecond\nthird\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		n    int64
		want string
	}{
		{1000, "first line\nsecond\nthird\n"}, // whole file
		{13, "second\nthird\n"},               // window starts exactly on a line
		{12, "third\n"},                       // starts mid "second": the fragment is dropped
		{3, ""},                               // inside the last line: nothing complete
	}
	for _, c := range cases {
		b, fi, err := Read(path, c.n)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != c.want {
			t.Errorf("Read(%d) = %q, want %q", c.n, b, c.want)
		}
		if fi.Size() != 24 {
			t.Errorf("size = %d", fi.Size())
		}
	}
	if _, _, err := Read(filepath.Join(t.TempDir(), "missing"), 10); err == nil {
		t.Error("missing file: want error")
	}
}
