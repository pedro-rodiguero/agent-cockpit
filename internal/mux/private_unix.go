//go:build !windows

package mux

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

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
