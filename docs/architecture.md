# Architecture

cockpit is one Go binary with three roles, plus an optional menu bar app.

```
                 ┌──────────────────────────── cockpit (dashboard) ───────────────────────────┐
 Claude Code ──┐ │  source.Collector ──► Snapshot ──┐                                          │
 Codex       ──┼─┼─►  (every 2s, in parallel)       ├──► tui.App ──► grid / list ──► terminal  │
 agy         ──┤ │                                  │      ▲  │                                 │
 cockpit-      │ │  chat.Reader (transcripts) ──────┘      │  │ keys, spawn, resize            │
 source-* ─────┘ │                                         │  ▼                                 │
                 │                                     mux.Client                             │
                 └─────────────────────────────────────────┬──────────────────────────────────┘
                                                           │ Unix socket, newline JSON
                 ┌──────────────── cockpit serve ──────────┴─────────┐
                 │  mux.Server: per pane a PTY + VT emulator          │──► claude / codex / agy / shell
                 └────────────────────────────────────────────────────┘

 Agent Cockpit.app ──► runs `cockpit json` every 3s ──► menu + notifications
```

## Packages

| package | responsibility |
|---------|----------------|
| `cmd/cockpit` | command-line entry point: dashboard, `serve`, `panes`, `json`, `open`, `config` |
| `internal/model` | `Session`, `State`, `Snapshot`: the types every other package shares |
| `internal/source` | finding sessions: one `Source` per agent, external sources, and the `Collector` that merges them |
| `internal/config` | the settings file (`config.json`), read again whenever it changes |
| `internal/todo` | the to-do list: tasks in a JSON file, quick-add parsing, history, tombstones and merging |
| `internal/todosync` | keeping the list in step with a to-do server |
| `internal/auth` | sign-in building blocks: password hashing and policy, TOTP, sealing, rate limits, client IP |
| `internal/todoserver` | the to-do server: account, sessions, API, sync endpoint, and the phone app in `web/` |
| `cmd/cockpit-todo-server` | the to-do server's entry point: `serve`, `set-password`, `reset-2fa` |
| `internal/chat` | turning a transcript into short chat lines for a card |
| `internal/mux` | the pane server, its client, and the renderer that turns an emulator screen into themed ANSI rows |
| `internal/tui` | the dashboard: raw terminal I/O, layout, drawing, keys |
| `internal/peer` | sending a message into a running Claude Code session |
| `internal/term` | Terminal.app and iTerm2 automation: focus a tab, set tab titles |
| `internal/tailfile` | reading the end of an append-only log on a line boundary |
| `menubar` | the Mac app: menu bar status, notifications, the dashboard in a SwiftTerm window, and the Settings window |

`tui` uses `config`, `source`, `todo`, `chat`, `mux`, `peer` and `term`; `source` uses `config` and
`todo`, for the tasks source; `config` uses `todo` for its settings type. None of those use `tui`, and
`model` uses nothing.

## Collecting sessions

`Collector.Collect` reads the process table once, hands it to every source, and runs the sources in
parallel with a timeout each. It then applies the one rule that is the same for every agent (a busy
session whose log has not changed for `StuckAfter` becomes stuck) and sorts by urgency.

Sources are polled, not watched. Polling every two seconds is simple, survives agents being restarted
or upgraded, and costs little because each source caches what it parsed by file size and modification
time and only re-reads files that changed.

## The pane server

Agents in panes have to outlive the dashboard (closing a terminal tab must not stop a twenty-minute
turn), so the processes belong to a separate long-lived server rather than to the dashboard.

For each pane the server keeps a PTY and a VT emulator (`charmbracelet/x/vt`). Output from the program
goes into the emulator; the server renders the visible screen into one ANSI string per row and sends it
to dashboards when it changes. Colors the program leaves at "default", and the 16 basic ANSI colors, are
mapped to cockpit's palette so a pane looks like part of the dashboard rather than a stock xterm. The
emulator also answers terminal queries (cursor position, device attributes), which some agents wait on
at startup.

The dashboard resizes a pane to the size of its card, so the agent lays itself out for the space it
actually has; zooming a card resizes the pane again.

## The dashboard

`internal/tui` draws directly with escape sequences instead of using a TUI framework. Pane screens arrive
as finished ANSI rows that have to be placed inside card borders cell-exactly, and the whole frame is
diffed line by line and written inside a synchronized-update block (`CSI ? 2026 h/l`) so a 30 fps pane
does not flicker. That is straightforward with a small renderer and awkward through a framework's
layout model.

The dashboard is a single event loop (`App.Run`). Key presses, collected snapshots, pane updates and
window resizes arrive as events on one channel; only the loop touches `App` state, and drawing happens
at most every 33 ms when something changed.

Input goes through a stateful decoder: escape sequences, UTF-8 characters and bracketed pastes that
arrive split across reads are held until complete, and a lone ESC becomes the Esc key after a short
timeout. A paste is always one event, so pasted text can never be read as dashboard commands.

Keys go to the dashboard in navigation mode. In insert mode they go to the selected pane, except
`ctrl-q`, which returns to navigation; pasted text is sent with the server's `paste` operation, which
brackets it only if the program in the pane enabled bracketed paste.

Each tile has a place. Sessions that are working, blocked on the person or recently active get cards;
inbox items, panes whose program exited and sessions idle for over an hour go to the rail, a column on
the right of the grid. The place is computed from the snapshot when tiles are rebuilt, and the grid
view orders tiles by place, so the layout only needs to know how many at the end belong in the rail.

The asked state is the dashboard's, not a source's: sources report what the agent says (its turn is
over), and the dashboard moves an idle session to asked when its transcript ends with a question.

Transcripts are read by the collect goroutine and delivered with each snapshot. Drawing reads only the
dashboard's state: it does no I/O, and the layout (grid size, page, card sizes) is computed in the loop
before each frame, which is also where pane resizes are sent from.

## Moving a session into a pane

A process cannot be handed from the terminal app to cockpit alive: its terminal belongs to the app that
opened it. Moving a session therefore means stopping it and resuming the conversation, which is safe
between turns. When the session is working or waiting for an answer, the move is scheduled instead and
runs on the first collection pass that finds it idle; a stuck session is moved at once.

`⏎` (or `p`) on a Claude Code or Codex card running in a terminal tab asks first, then:

1. builds the resume command (`claude --resume <id>` with the session's profile, or `codex resume <id>`);
2. sends `SIGHUP` to the session's process, as closing its tab would, escalating to `SIGTERM` and
   `SIGKILL` if it lingers, and waits until it is gone, so two copies never write the same transcript;
3. spawns a pane in the session's directory with that command.

Because the pane's process is the agent itself (the login shell `exec`s it), its pid matches the one the
agent writes in its session file, and the dashboard merges the pane and the session into one card.
