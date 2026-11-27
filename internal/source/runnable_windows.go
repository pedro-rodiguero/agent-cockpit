package source

import (
	"os"
	"path/filepath"
	"strings"
)

// runnable reports whether the file may be executed: on Windows that is said by its extension.
func runnable(fi os.FileInfo) bool {
	switch strings.ToLower(filepath.Ext(fi.Name())) {
	case ".exe", ".cmd", ".bat", ".com":
		return true
	}
	return false
}

// programName is a program's name as the person calls it, without the extension that makes it runnable.
func programName(file string) string { return strings.TrimSuffix(file, filepath.Ext(file)) }
