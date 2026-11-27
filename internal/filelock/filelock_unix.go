//go:build !windows

// Package filelock takes exclusive locks on files, so processes take turns at what the file stands for.
package filelock

import (
	"errors"
	"os"
	"syscall"
)

// Lock waits for an exclusive lock on f.
func Lock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

// TryLock takes an exclusive lock on f if no other process holds one, and reports whether it did.
func TryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

// Unlock releases the lock on f.
func Unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
