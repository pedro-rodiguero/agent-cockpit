package mux

import (
	"fmt"
	"net"
	"os"
	"syscall"
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

// ownDir returns dir's file info when it is a real directory (not a symbolic link) owned by this user.
func ownDir(dir string) (os.FileInfo, error) {
	fi, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory; refusing to use it", dir)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return nil, fmt.Errorf("%s belongs to another user; refusing to use it", dir)
	}
	return fi, nil
}

// samePeer reports an error unless the process at the other end of conn runs as this user.
func samePeer(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("not a Unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	uid, uerr := -1, error(nil)
	if err := raw.Control(func(fd uintptr) { uid, uerr = peerUID(int(fd)) }); err != nil {
		return err
	}
	if uerr != nil {
		return uerr
	}
	if uid != os.Getuid() {
		return fmt.Errorf("peer runs as uid %d", uid)
	}
	return nil
}
