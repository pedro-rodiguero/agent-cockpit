package tui

import (
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/config"
	"github.com/pedro-rodiguero/agent-cockpit/internal/quick"
)

// systemLookEvery is how often the system's appearance is checked when the theme follows it.
const systemLookEvery = 10 * time.Second

// appearanceReader turns the settings into an appearance. It is used by the collect goroutine only.
type appearanceReader struct {
	checked time.Time
	dark    bool
}

func (r *appearanceReader) read(cfg config.Config, now time.Time) appearance {
	light := cfg.Theme == config.Light
	if cfg.Theme == config.System {
		if now.Sub(r.checked) >= systemLookEvery {
			r.checked, r.dark = now, systemDark()
		}
		light = !r.dark
	}
	return appearance{light: light, terminalBg: cfg.TerminalBackground(os.Getenv("TERM_PROGRAM"))}
}

// systemDark reports whether macOS is in dark mode. Elsewhere there is no one place to ask, and the
// dashboard stays dark.
func systemDark() bool {
	if runtime.GOOS != "darwin" {
		return true
	}
	out, err := quick.Output("", "defaults", "read", "-g", "AppleInterfaceStyle")
	return err == nil && strings.TrimSpace(string(out)) == "Dark"
}

// shortDuration writes d the way the rail labels it: 45m, 1h, 1h30m.
func shortDuration(d time.Duration) string {
	s := strings.TrimSuffix(d.Round(time.Minute).String(), "0s") // 1h30m0s → 1h30m
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
