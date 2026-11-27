//go:build !windows

package source

import "os"

// runnable reports whether the file may be executed.
func runnable(fi os.FileInfo) bool { return fi.Mode()&0o111 != 0 }

// programName is a program's name as the person calls it.
func programName(file string) string { return file }
