package term

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/quick"
)

// CanFocus reports whether Focus can bring a terminal tab to the front on this system.
const CanFocus = true

// running reports whether a process named exactly app is running, so AppleScript never launches it.
func running(app string) bool {
	return quick.Run("", "pgrep", "-xq", app) == nil
}

// osascript runs an AppleScript with a timeout: an app showing a modal dialog never answers.
func osascript(script string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// q quotes s as an AppleScript string literal.
func q(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Focus brings the tab/session whose tty matches to the front. tty is "ttys006" or "/dev/ttys006".
func Focus(tty string) error {
	if tty == "" {
		return fmt.Errorf("session has no terminal")
	}
	dev := "/dev/" + strings.TrimPrefix(tty, "/dev/")
	if running("Terminal") {
		out, err := osascript(`tell application "Terminal"
	repeat with w in windows
		repeat with t in tabs of w
			if tty of t is ` + q(dev) + ` then
				set selected of t to true
				set index of w to 1
				activate
				return "ok"
			end if
		end repeat
	end repeat
end tell
return "none"`)
		if err == nil && out == "ok" {
			return nil
		}
	}
	if running("iTerm2") {
		out, err := osascript(`tell application "iTerm2"
	repeat with w in windows
		repeat with t in tabs of w
			repeat with s in sessions of t
				if tty of s is ` + q(dev) + ` then
					select w
					select t
					select s
					activate
					return "ok"
				end if
			end repeat
		end repeat
	end repeat
end tell
return "none"`)
		if err == nil && out == "ok" {
			return nil
		}
	}
	return fmt.Errorf("no Terminal/iTerm2 tab owns %s", tty)
}

// Label sets the tab title for each tty (tty → title). Empty title clears the custom title.
func Label(titles map[string]string) error {
	if len(titles) == 0 {
		return nil
	}
	var cases strings.Builder
	for tty, title := range titles {
		dev := "/dev/" + strings.TrimPrefix(tty, "/dev/")
		fmt.Fprintf(&cases, "if tty of t is %s then set custom title of t to %s\n", q(dev), q(title))
	}
	if running("Terminal") {
		if _, err := osascript(`tell application "Terminal"
	repeat with w in windows
		repeat with t in tabs of w
			` + cases.String() + `
		end repeat
	end repeat
end tell`); err != nil {
			return err
		}
	}
	if running("iTerm2") {
		var ic strings.Builder
		for tty, title := range titles {
			dev := "/dev/" + strings.TrimPrefix(tty, "/dev/")
			fmt.Fprintf(&ic, "if tty of s is %s then set name of s to %s\n", q(dev), q(title))
		}
		_, err := osascript(`tell application "iTerm2"
	repeat with w in windows
		repeat with t in tabs of w
			repeat with s in sessions of t
				` + ic.String() + `
			end repeat
		end repeat
	end repeat
end tell`)
		return err
	}
	return nil
}

// Open opens a URL in the default browser.
func Open(url string) error { return quick.Run("", "open", url) }
