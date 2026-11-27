package mux

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A folder someone else made first (here one of root's) is never used for the socket, by the server or
// the client: on Linux the fallback lives under a /tmp every user can write to.
func TestSocketFolderMustBeOwnAndPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps other accounts out with the profile folder's permissions, not an owner and a mode")
	}
	if os.Getuid() == 0 {
		t.Skip("root owns the folder used as someone else's")
	}
	for _, dir := range []string{"/usr", "/"} {
		if err := ensurePrivateDir(dir); err == nil || !strings.Contains(err.Error(), "another user") {
			t.Fatalf("ensure %s: %v", dir, err)
		}
		if err := checkPrivateDir(dir); err == nil {
			t.Fatalf("check %s passed", dir)
		}
	}

	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := ensurePrivateDir(real); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := checkPrivateDir(link); err == nil {
		t.Fatal("a symbolic link passed")
	}

	if err := os.Chmod(real, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := checkPrivateDir(real); err == nil {
		t.Fatal("a folder anyone can enter passed the client's check")
	}
	if err := ensurePrivateDir(real); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o700 {
		t.Fatalf("own folder left at %o", fi.Mode().Perm())
	}
}

// Without a cache folder, the fallback under the temporary folder is per user, not one shared name.
func TestDirFallbackIsPerUser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps other accounts out with the profile folder's permissions, not an owner and a mode")
	}
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	if got, want := Dir(), filepath.Join(os.TempDir(), fmt.Sprintf("agent-cockpit-%d", os.Getuid())); got != want {
		t.Fatalf("Dir() = %s, want %s", got, want)
	}
}

func TestSamePeerOnAUnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		done <- samePeer(c)
	}()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := samePeer(c); err != nil {
		t.Fatalf("client side: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("server side: %v", err)
	}
}
