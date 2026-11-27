package mux

import (
	"fmt"
	"net"
	"os"
)

// ownDir returns dir's file info when it is a real directory (not a symbolic link). Windows has no owner
// and mode to read the way Unix does: the folder is under the person's profile (%LocalAppData%), which
// other accounts cannot enter unless its permissions were changed by hand.
func ownDir(dir string) (os.FileInfo, error) {
	fi, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory; refusing to use it", dir)
	}
	return fi, nil
}

// samePeer accepts the connection: Windows does not say who is at the other end of a Unix socket, so the
// folder the socket is in, which only this person can enter, is what keeps others out.
func samePeer(net.Conn) error { return nil }

// private is always true on Windows, whose folders have no mode to read: see ownDir.
func private(os.FileInfo) bool { return true }
