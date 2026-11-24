# Configuration

cockpit reads `config.json` in its folder under the user config directory:
`~/Library/Application Support/agent-cockpit/` on macOS, `$XDG_CONFIG_HOME/agent-cockpit/` (usually
`~/.config/agent-cockpit/`) on Linux. The to-do list, `todo.json`, sits next to it.

cockpit only reads the file. The menu bar app's **Settings** window (`⌘,`, or Settings… in the menu)
writes it, keeping keys it does not know. Every field is optional, and a missing file means the
defaults. While the file does not parse, cockpit keeps the last settings it read and says so on stderr,
and the Settings window refuses to save over it.

`cockpit config` prints the path, the settings in effect, and what cockpit detected on the machine: the
Claude Code profiles, the Codex and agy folders, and every source it can collect from.

## Fields

| field | default | |
|-------|---------|---|
| `theme` | `dark` | `dark`, `light`, or `system` (follows macOS; dark elsewhere) |
| `background` | the theme's | `terminal` leaves the page background to the terminal, so a Terminal.app or iTerm profile's transparency or image shows through. Cards keep their own background |
| `quietAfter` | `1h` | how long a session may sit idle before its card moves to the rail |
| `stuckAfter` | `10m` | how long a working session may stay silent before it counts as stuck |
| `accounts.claude` | detected | Claude Code config dirs, default profile first. Unset: `$CLAUDE_CONFIG_DIR` or `~/.claude`, then every `~/.claude-<name>` holding a `sessions/` folder |
| `accounts.codex` | `$CODEX_HOME` or `~/.codex` | Codex's folder |
| `accounts.agy` | `~/.gemini/antigravity-cli` | agy's folder |
| `sources.off` | none | names of sources not to collect from: `claude`, `codex`, `agy`, `tasks`, or an external source's name |
| `todo.defaultGroup` | `Tasks` | the group of a task added without `#group` |
| `todo.fold` | none | shows one group's tasks inside another, marked with their own group: `{"Side project": "Personal"}` |
| `todo.foldWhenReachable` | none | `host:port`; with it, `fold` applies only while that address accepts connections (checked at most every 30s), for a list that should look different on the office network |
| `app.opacity` | `1` | the app window's background, `0.5`–`1`. Below 1 the page background is left to the window, as with `background: terminal`, only inside the app |
| `app.blur` | `true` | blur what is behind a translucent app window |
| `app.font` | the system's monospaced font | a monospaced font family for the app window |
| `app.fontSize` | `13` | |
| `app.notify` | `true` | the app's notifications |
| `app.hotKey` | `option+space` | the shortcut that brings the dashboard up from any app and hides it again: modifiers (`control`, `option`, `shift`, `command`) and a key (a letter, a digit, `space`, `grave`) joined by `+`, or `off` |

Durations are Go durations: `90s`, `45m`, `1h30m`.

The to-do settings live here rather than in `todo.json` because they belong to the machine (an office
address) while the list is meant to be shared between devices.

## When changes apply

- `theme`, `background`, `quietAfter`, `todo.*` and `app.opacity`: on the dashboard's next collection
  pass, within a couple of seconds. `system` checks the system's appearance every 10 seconds.
- `app.*`: at once in the app window.
- `accounts`, `sources` and `stuckAfter`: when the dashboard starts. The app restarts its window's
  dashboard after a change made in Settings; panes keep running, since they belong to the pane server.

## Environment

`COCKPIT_CLAUDE_DIRS`, `COCKPIT_SOURCES` and `COCKPIT_STUCK_AFTER` override `accounts.claude`,
`sources.off` and `stuckAfter`. See the README.

## Open at login

Not a field: the app opens at login through a LaunchAgent,
`~/Library/LaunchAgents/io.github.pedro-rodiguero.agent-cockpit.plist`, which `scripts/install.sh`
writes and the Settings window's General tab adds or removes. At login the app starts in the menu bar
only; opened any other way (Spotlight, Finder, the Dock) it shows the dashboard.
