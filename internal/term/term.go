// Package term drives the terminal apps around cockpit's sessions: bring the tab that owns a tty to the
// front, write a readable title on each tab, and open links.
//
// On macOS it talks to Terminal.app and iTerm2 through AppleScript. Elsewhere no terminal exposes its
// tabs that way, so Focus reports ErrUnsupported, Label does nothing and Open uses xdg-open.
package term

import "errors"

// ErrUnsupported is Focus's error on systems where cockpit cannot reach a terminal's tabs.
var ErrUnsupported = errors.New("going to a terminal tab works on macOS only")
