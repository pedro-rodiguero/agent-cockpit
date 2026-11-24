# agent-cockpit

A terminal dashboard for people who run many coding-agent sessions at once.

When a dozen Claude Code, Codex and Antigravity (`agy`) sessions live in a dozen terminal tabs, every
tab looks the same: you can't tell which one is waiting for an answer, which one has been "thinking"
for twenty minutes, or which one finished while you were elsewhere. cockpit finds those sessions on its
own, shows each one as a card with its live conversation, and keeps what needs you in front.

```
 ◆ AGENT COCKPIT  grid 2×1                                                                         14:32:07
 ▲ 1 waiting   ? 1 your turn   ✕ 0 stuck?   ✉ 1 inbox   ● 0 working   ○ 2 idle   ◧ 0 panes
╭ ▲ Login rate limit ──── claude · 1 ╮╭ ? Auth snapshots ─ claude work · 2 ╮│ INBOX                       1
│ ~/src/api  ⎇ feat/limits  212k ctx ││ ~/src/web  ⎇ main          88k ctx ││ ✉ payments PR review       3h
│ › add rate limiting to login       ││ › why do the snapshots fail on CI? ││   pull request · #412
│ ⏺ Edit ~/src/api/limits.ts         ││ Two snapshots read the clock; I    ││
│ Added a sliding-window limiter,    ││ froze it in the test setup.        ││ IDLE 1h+                    2
│ five tries a minute per IP.        ││ ⋯                                  ││ ○ Migrate to Postgres 17  19h
│                                    ││ Should I also pin the timezone?    ││   ~/src/db · s002
│ ▲ waiting 2m · permission     plan ││ ? your turn 14m               high ││ ○ Release notes for 2.3    2d
╰────────────────────────────────────╯╰────────────────────────────────────╯│   ~/src/app · s009
 ⏎ continue here   o go to tab   r reply   z zoom   │  n new   ! next needing me   v list   ? keys
```

## What it does

- **Finds sessions without setup.** It reads the state files that Claude Code and Codex already write,
  and the process table; nothing has to be started through cockpit.
- **Shows what each session is doing.** Every card is titled with what the session is about and has
  the directory and git branch, the model and how much context it holds, your last prompt, the agent's
  last reply (from its first line, with a closing question kept in view), and a state line with the
  tool call running right now:

  | glyph | state     | meaning |
  |-------|-----------|---------|
  | ▲ | waiting   | the agent is blocked on a question or a permission prompt |
  | ? | your turn | the turn ended with the agent asking you something |
  | ✕ | stuck?    | working, but nothing written for a while (`COCKPIT_STUCK_AFTER`, default 10m) |
  | ✉ | inbox     | an item from an external source (see below) |
  | ● | working   | a turn is in progress |
  | ○ | idle      | the turn ended; your move |

- **Keeps the grid for what is happening.** Sessions idle for over an hour, inbox items and panes whose
  program exited move to a rail on the right, two rows each, so the cards go to the sessions that are
  working or waiting for you. The grid picks its columns and rows from the window size and pages when
  the cards don't fit. `v` switches to a list with a reading pane for the selected session.
- **Runs agents in panes.** `n` starts Claude Code, Codex, agy or a shell inside a card: a real
  interactive terminal. The panes belong to a small background server (`cockpit serve`), so they keep
  running after you close the dashboard and come back when you reopen it. `⏎` on a pane whose program
  exited reopens it, resuming the same conversation when cockpit knows which one it was.
- **Continues any session here.** `⏎` on a Claude Code or Codex session running in a terminal tab
  offers to continue it in cockpit: once you confirm, the agent in the tab stops and the same
  conversation resumes in a pane, in the same directory and with the same Claude profile, so memory,
  skills, hooks and MCP servers are the ones the session already had. You are typing into it right away.
  A session that is mid-turn or waiting for an answer is not cut off: it moves when its turn ends, and the
  card says so until then (`⏎` on it again cancels).
- **Talks back without moving anything.** `o` jumps to a session's Terminal.app or iTerm2 tab; `r` sends a
  message to a Claude Code session without leaving the dashboard.
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
| `←↑↓→` `hjkl` | move; right from the last column enters the rail · `tab` next · `1`–`9` card on this page · `[` `]` page |
| `⏎` | type into a pane · continue a tab's session here (asks first) · reopen an ended pane · open an inbox item |
| `o` | go to the session's terminal tab |
| `ctrl-q` | leave a pane (every other key goes to the agent) |
| `n` | new pane: `↑↓` picks the agent (one entry per Claude profile), `tab` completes the folder |
| `r` | send a message to a Claude Code session |
| `z` | zoom the selected card |
| `x` | close a pane (stops its agent) |
| `!` | next card that needs you · `a` show only those |
| `v` | grid or list · `t` tab titles on/off · `q` quit (panes keep running) |
| `?` | every key; the footer always shows the ones for the selected card |

## Configuration

| variable | default | |
|----------|---------|---|
| `COCKPIT_STUCK_AFTER` | `10m` | how long a working session may stay silent before it counts as stuck |
| `COCKPIT_CLAUDE_DIRS` | `~/.claude` and every `~/.claude-*` | Claude Code config dirs, colon-separated, default profile first |
| `COCKPIT_SOURCES` | all | comma-separated sources to enable (`claude,codex,agy,<external>`) |
| `COCKPIT_COLOR` | detected | `truecolor` or `256`; cockpit uses 24-bit color only where the terminal announces it (Terminal.app before macOS 26 does not) |

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
