// Package tailfile reads the end of append-only logs, such as agent transcripts.
package tailfile

import (
	"bytes"
	"io"
	"os"
)

// Read returns at most n bytes from the end of path, starting at a line boundary so the first line is
// never a fragment. It also returns the file's info as of the read.
func Read(path string, n int64) ([]byte, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	// Start one byte early: if that byte is a newline, the window begins on a whole line.
	off := max(0, fi.Size()-n-1)
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, nil, err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, nil, err
	}
	if off > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			return nil, fi, nil
		}
		b = b[i+1:]
	}
	return b, fi, nil
}
