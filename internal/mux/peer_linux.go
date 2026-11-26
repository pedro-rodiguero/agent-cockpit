package mux

import "golang.org/x/sys/unix"

// peerUID is the user the process at the other end of a Unix socket runs as.
func peerUID(fd int) (int, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return -1, err
	}
	return int(cred.Uid), nil
}
