package mux

import (
	"fmt"
	"os"
)

// The socket accepts "run this command", so whoever can reach it can run anything as the person. It
// lives in a directory only they can enter, and both ends check that: the server so nobody else can
// connect, the client so it never sends keystrokes to a server someone else started in a directory they
// made first (a fallback under a shared /tmp on Linux).

// ensurePrivateDir creates dir if needed and checks that it is private, tightening its permissions when
// it belongs to this user but others can enter it.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := ownDir(dir)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// checkPrivateDir reports an error unless dir is a directory of this user's that nobody else can enter.
func checkPrivateDir(dir string) error {
	fi, err := ownDir(dir)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s can be entered by other users (mode %o); refusing to use it", dir, fi.Mode().Perm())
	}
	return nil
}
