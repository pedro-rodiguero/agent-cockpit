package term

import "os/exec"

// CanFocus reports whether Focus can bring a terminal tab to the front on this system.
const CanFocus = false

// Focus would bring the tab that owns tty to the front; see ErrUnsupported.
func Focus(string) error { return ErrUnsupported }

// Label would set tab titles; without a way to reach the tabs it does nothing.
func Label(map[string]string) error { return nil }

// Open opens a URL in the default browser. The URL is handed to the system's handler as one argument,
// never to a shell.
func Open(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run()
}
