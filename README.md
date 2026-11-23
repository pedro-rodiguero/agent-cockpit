# agent-cockpit

A terminal dashboard for people who run many coding-agent sessions at once.

When a dozen Claude Code, Codex and Antigravity (`agy`) sessions live in a dozen terminal tabs, every
tab looks the same: you can't tell which one is waiting for an answer, which one has been "thinking"
for twenty minutes, or which one finished while you were elsewhere. cockpit finds those sessions on its
own, shows each one as a card with its live conversation, and sorts what needs you to the top.

```
 ◆ cockpit   grid 3×2 · page 1/1                                   14:32   ▲ 1  ✕ 0  ● 3  ○ 2
┌ ▲ api-auth · claude ─────────── s004 · 2m · 1 ┐┌ ● web-next · claude ──────── s007 · 14m · 2 ┐
│ › add rate limiting to the login route        ││ ⏺ Bash npm run test -- auth                  │
│ ⏺ Edit ~/src/auth/limits.ts                   ││ The suite passes except for two snapshot     │
│ Done. Want me to wire it into the gateway?    ││ tests that depend on the clock.              │
│ ▲ waiting · permission prompt                 ││ ● working · 14m                              │
└──────────────────────── ⏎ go to tab · p pane ┘└───────────────────────────────────────────────┘
```

## What it does

- **Finds sessions without setup.** It reads the state files that Claude Code and Codex already write,
  and the process table; nothing has to be started through cockpit.
- **Shows what each session is doing.** Every card shows the end of the conversation (your prompts, the
  agent's replies, the tools it ran) and its state:

  | glyph | state   | meaning |
  |-------|---------|---------|
  | ▲ | waiting | the agent asked a question or a permission |
  | ✕ | stuck?  | working, but nothing written for a while (`COCKPIT_STUCK_AFTER`, default 10m) |
  | ✉ | inbox   | an item from an external source (see below) |
  | ● | working | a turn is in progress |
  | ○ | idle    | the turn ended; your move |

- **Fits the window.** The grid picks its columns and rows from the terminal size and pages when the
  cards don't fit. `v` switches to a dense list.
- **Runs agents in panes.** `n` starts Claude Code, Codex, agy or a shell inside a card: a real
  interactive terminal. The panes belong to a small background server (`cockpit serve`), so they keep
  running after you close the dashboard and come back when you reopen it.
- **Moves a session into a pane.** `p` stops a Claude Code or Codex session in its terminal tab and
  resumes the same conversation in a pane, in the same directory and with the same Claude profile, so
  memory, skills, hooks and MCP servers are the ones the session already had.
- **Talks back.** `⏎` jumps to a session's Terminal.app or iTerm2 tab; `r` sends a message to a Claude
  Code session without leaving the dashboard.
- **Labels your tabs.** Each terminal tab's title shows its session's state and name (`t` toggles it).
- **Menu bar app (optional).** `◆ ▲1 ●3` in the menu bar, a list of sessions, and a macOS notification
  when a session starts waiting, looks stuck, or finishes a turn that took more than a minute.

## Requirements

- macOS 13 or later (tab focus and titles use AppleScript; the menu bar app is AppKit)
- Go 1.26 to build, and the Xcode command line tools for the menu bar app
- Any of: [Claude Code](https://docs.claude.com/en/docs/claude-code), Codex CLI, Antigravity CLI

## Install

```sh
git clone https://github.com/pedro-rodiguero/agent-cockpit
cd agent-cockpit
./scripts/install.sh                # cockpit → ~/.local/bin, Agent Cockpit.app → ~/Applications
NO_MENUBAR=1 ./scripts/install.sh   # only the cockpit binary
```

`PREFIX` and `APP_DIR` change the install locations. The first time cockpit focuses a tab, macOS asks
whether your terminal may control Terminal.app or iTerm2.

## Use

```sh
cockpit            # the dashboard
cockpit panes      # panes held by the server (-screen prints their screens)
cockpit json       # one snapshot as JSON
cockpit open <id>  # focus a session's tab, or open an inbox item's link
```

| key | action |
|-----|--------|
| `←↑↓→` `hjkl` | move · `tab` next · `1`–`9` card on this page · `[` `]` page |
| `⏎` | type into a pane · go to a session's tab · open an inbox item |
| `ctrl-q` | leave a pane (every other key goes to the agent) |
| `n` | new pane: `↑↓` picks the agent (one entry per Claude profile), `tab` completes the folder |
| `p` | move a Claude Code or Codex session from its tab into a pane |
| `r` | send a message to a Claude Code session |
| `z` | zoom the selected card |
| `x` | close a pane (stops its agent) |
| `!` | next card that needs you · `a` show only those |
| `v` | grid or list · `t` tab titles on/off · `q` quit (panes keep running) |

## Configuration

| variable | default | |
|----------|---------|---|
| `COCKPIT_STUCK_AFTER` | `10m` | how long a working session may stay silent before it counts as stuck |
| `COCKPIT_CLAUDE_DIRS` | `~/.claude` and every `~/.claude-*` | Claude Code config dirs, colon-separated, default profile first |
| `COCKPIT_SOURCES` | all | comma-separated sources to enable (`claude,codex,agy,<external>`) |

A Claude profile other than the default one can keep a long-lived OAuth token (from
`claude setup-token`) in `<profile dir>/token`; panes started for that profile use it.

## Sources

| source | where the state comes from | precision |
|--------|----------------------------|-----------|
| claude | `<config>/sessions/<pid>.json`, kept current by Claude Code, plus the transcript | exact |
| codex  | the rollout `.jsonl` the `codex` process writes; the last turn event | exact for turns |
| agy    | the conversation database the `agy` process holds open; written in the last 20s means working | estimated, never "waiting" |

Anything else (a review queue, an issue tracker, a team inbox) can show up as cards through an
**external source**: an executable named `cockpit-source-<name>` on your `PATH` that prints a JSON array
of sessions. See [docs/sources.md](docs/sources.md).

## How it works

[docs/architecture.md](docs/architecture.md) explains the parts and how they fit;
[docs/protocol.md](docs/protocol.md) describes the pane server's wire format.

Some of what cockpit reads (Claude Code's session files and messaging socket, Codex rollouts) is
internal to those tools rather than a documented interface. [docs/compatibility.md](docs/compatibility.md)
lists what cockpit depends on and the versions it was checked against.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
