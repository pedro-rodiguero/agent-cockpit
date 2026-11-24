package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/config"
)

func TestUseTheme(t *testing.T) {
	defer useTheme(appearance{})
	useTheme(appearance{light: true})
	if cBg != (rgb{0xf6, 0xf1, 0xe6}) || look("waiting").color != cWaiting || palette256[cBg] != 255 {
		t.Errorf("light theme not applied: bg %v, 256 %d", cBg, palette256[cBg])
	}
	useTheme(appearance{})
	if cBg != (rgb{0x14, 0x11, 0x0c}) || palette256[cBg] != 233 {
		t.Errorf("dark theme not applied: %v", cBg)
	}
	seen := map[rgb]bool{}
	for _, tc := range themeColors {
		if seen[tc.dark] {
			t.Errorf("two dark colors are %v: palette256 would map one of them wrong", tc.dark)
		}
		seen[tc.dark] = true
	}
}

func TestTerminalBackgroundLeavesPageUnpainted(t *testing.T) {
	defer useTheme(appearance{})
	useTheme(appearance{terminalBg: true})
	if s := paint(cInk, cBg, "x"); strings.Contains(s, "48;2") {
		t.Errorf("page background painted: %q", s)
	}
	if s := paint(cInk, cPanel, "x"); !strings.Contains(s, "48;2") {
		t.Errorf("panel background not painted: %q", s)
	}
}

func TestAppearanceReader(t *testing.T) {
	var r appearanceReader
	now := time.Now()
	if ap := r.read(config.Config{Theme: config.Light}, now); !ap.light {
		t.Error("light theme read as dark")
	}
	if ap := r.read(config.Config{}, now); ap.light {
		t.Error("default theme is not dark")
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{time.Hour: "1h", 45 * time.Minute: "45m", 90 * time.Minute: "1h30m", 2 * time.Hour: "2h"} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
