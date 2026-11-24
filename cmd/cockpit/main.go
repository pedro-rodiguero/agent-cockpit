// Command cockpit shows every coding-agent session on the machine in one terminal dashboard and runs
// agents in panes that outlive it. Run `cockpit help` for the subcommands.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/config"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
	"github.com/pedro-rodiguero/agent-cockpit/internal/source"
	"github.com/pedro-rodiguero/agent-cockpit/internal/term"
	"github.com/pedro-rodiguero/agent-cockpit/internal/tui"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `cockpit: one view of your coding-agent sessions (Claude Code, Codex, agy)

Usage:
  cockpit [flags]          open the dashboard
  cockpit serve            run the pane server (started automatically; keeps panes alive)
  cockpit panes [-screen]  list the panes the server holds, optionally with their screens
  cockpit json             print the current snapshot as JSON (used by the menu bar app)
  cockpit open <id|tty>    bring a session's terminal tab to the front, or open an item's link
  cockpit config           print the settings in effect, what was detected, and the settings file's path
  cockpit version          print the version

Dashboard flags:
  -interval 2s   how often sessions are collected
  -no-labels     don't write session state into terminal tab titles
  -list          start in the list view instead of the grid

Settings: <user config dir>/agent-cockpit/config.json (see docs/configuration.md). These variables
override it:
  COCKPIT_SOURCES       comma-separated sources to enable (default: all but the ones turned off)
  COCKPIT_STUCK_AFTER   how long a working session may stay silent before it counts as stuck (10m)
  COCKPIT_CLAUDE_DIRS   colon-separated Claude Code config dirs (default: ~/.claude and ~/.claude-*)

External sources: any executable named cockpit-source-<name> on PATH. See docs/sources.md.
`

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "", "tui":
		err = runDashboard(args)
	case "serve":
		err = mux.Serve()
	case "panes":
		err = runPanes(args)
	case "json":
		err = runJSON()
	case "open", "focus":
		err = runOpen(args)
	case "config":
		err = runConfig()
	case "version", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cockpit:", err)
		os.Exit(1)
	}
}

func runDashboard(args []string) error {
	fs := flag.NewFlagSet("cockpit", flag.ExitOnError)
	interval := fs.Duration("interval", 2*time.Second, "")
	noLabels := fs.Bool("no-labels", false, "")
	list := fs.Bool("list", false, "")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	_ = fs.Parse(args)
	file := settings()
	return tui.Run(source.Default(file), tui.Options{Interval: *interval, LabelTabs: !*noLabels, Grid: !*list, Settings: file})
}

// runPanes prints the server's panes. It never starts a server: with none running there are no panes.
func runPanes(args []string) error {
	withScreen := len(args) > 0 && args[0] == "-screen"
	mc, err := mux.Connect()
	if errors.Is(err, mux.ErrNotRunning) {
		fmt.Println("no pane server running")
		return nil
	}
	if err != nil {
		return err
	}
	defer mc.Close()

	// On attach the server sends the pane list followed by one screen per pane.
	ready := make(chan struct{}, 1)
	mc.OnUpdate = func(mux.Msg) {
		panes := mc.Panes()
		for _, p := range panes {
			if mc.Screen(p.ID) == nil {
				return
			}
		}
		select {
		case ready <- struct{}{}:
		default:
		}
	}
	go func() { _ = mc.Run() }()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
	}

	for _, pn := range mc.Panes() {
		state := "running"
		if pn.Exited {
			state = fmt.Sprintf("exited %d", pn.ExitCode)
		}
		fmt.Printf("%s\t%s\t%s\tpid %d\t%dx%d\t%s\n", pn.ID, pn.Agent, pn.Cwd, pn.PID, pn.Cols, pn.Rows, state)
		if scr := mc.Screen(pn.ID); withScreen && scr != nil {
			for _, l := range scr.Lines {
				fmt.Println("  │ " + strings.TrimRight(ansi.Strip(l), " "))
			}
		}
	}
	return nil
}

func runJSON() error {
	snap := source.Default(settings()).Collect(context.Background())
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(snap)
}

// runOpen focuses a terminal tab given a session id or a tty, or opens the link of an inbox item.
func runOpen(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: cockpit open <id|tty>")
	}
	target := args[0]
	if strings.HasPrefix(target, "tty") || strings.HasPrefix(target, "/dev/") {
		return term.Focus(target)
	}
	snap := source.Default(settings()).Collect(context.Background())
	for _, s := range snap.Sessions {
		switch {
		case s.ID != target:
			continue
		case s.TTY != "":
			return term.Focus(s.TTY)
		case s.URL != "":
			return term.Open(s.URL)
		default:
			return fmt.Errorf("session %s has no terminal tab or link", target)
		}
	}
	return fmt.Errorf("session %s not found", target)
}

// settings is the settings file. One that does not parse is reported and otherwise ignored.
func settings() *config.File {
	f := &config.File{Path: config.DefaultPath()}
	if _, err := f.Get(); err != nil {
		fmt.Fprintln(os.Stderr, "cockpit: settings ignored:", err)
	}
	return f
}

// runConfig prints the settings in effect and what cockpit found on the machine, for people and for the
// menu bar app's Preferences window.
func runConfig() error {
	file := &config.File{Path: config.DefaultPath()}
	cfg, err := file.Get()
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	var names []string
	for _, s := range source.Available(cfg) {
		names = append(names, s.Name())
	}
	out := struct {
		Path     string        `json:"path"`
		Error    string        `json:"error,omitempty"`
		Settings config.Config `json:"settings"`
		Detected struct {
			Claude  []string `json:"claude"`
			Codex   string   `json:"codex"`
			Agy     string   `json:"agy"`
			Sources []string `json:"sources"`
		} `json:"detected"`
	}{Path: file.Path, Error: errText, Settings: cfg}
	out.Detected.Claude = source.DetectClaudeDirs()
	out.Detected.Codex = source.CodexHome(config.Config{})
	out.Detected.Agy = source.AgyRoot(config.Config{})
	out.Detected.Sources = names
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
