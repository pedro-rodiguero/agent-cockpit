# Pane server protocol

`cockpit serve` owns the panes. Each pane is a program running on a pseudo-terminal, with a VT emulator
that keeps its screen. Dashboards connect to the server, draw the screens it sends, and forward the keys
the person types. This file describes that connection; the Go types are in
[`internal/mux/proto.go`](../internal/mux/proto.go).

## Transport

- A Unix socket at `~/Library/Caches/agent-cockpit/mux.sock` (`$XDG_CACHE_HOME/agent-cockpit/` on
  Linux), mode `0600`, so only the user who started the server can connect. When that path is longer
  than a Unix socket allows (103 bytes, with a long home directory), the socket goes in
  `$TMPDIR/agent-cockpit-<uid>/` instead.
- The folder holding the socket must be a real directory owned by the user that nobody else can enter:
  the server creates it so (and tightens its own folder's mode), and both the server and the dashboard
  refuse one that is a link, belongs to someone else or is open to others. The server also drops any
  connection from another user, and the dashboard checks the server's user, by the socket's peer
  credentials.
- Each message is one JSON object followed by a newline, in both directions.
- The dashboard starts the server when nothing answers on the socket (`cockpit serve`, in its own
  session so it survives the terminal that started it). The server logs to `serve.log` in the cache
  folder and exits after ten minutes with no panes and no connected dashboards.

## Messages

Every message has an `op`; the other fields depend on it.

### Dashboard to server

| op | fields | effect |
|----|--------|--------|
| `attach` | | subscribe to updates; the server replies with `panes` and one `screen` per pane |
| `spawn` | `agent`, `cwd`, `args`, `env`, `cols`, `rows` | start a pane; replies `spawned` or `error` |
| `input` | `id`, `data` | write bytes to the pane's terminal (`data` is base64, as JSON encodes bytes) |
| `paste` | `id`, `data` | paste text: wrapped in bracketed-paste markers only if the program enabled that mode |
| `resize` | `id`, `cols`, `rows` | resize the pane; the program receives `SIGWINCH` |
| `close` | `id` | `SIGHUP` the program (then `SIGKILL` after 3s) and drop the pane |
| `shutdown` | | exit, so a newer server can take over; refused with `error` while a pane's program is running |
| `history` | `id`, `rows` | ask for the pane's screen as it was `rows` lines further up; replies `history` |
| `wheel` | `id`, `x`, `y`, `up` | turn the wheel one notch at column `x`, row `y` (from 0): a mouse report if the program asked for mouse events (DECSET 9, 1000, 1002 or 1003; SGR encoding with 1006), else three arrow keys if it is on the alternate screen, else nothing |
| `select` | `id` | pass the request on to every attached dashboard, which selects session `id` (or the pane it runs in) once it has collected it. Any connection may send it without attaching: `cockpit select` does |

`agent` is one of `claude`, `codex`, `agy`, `shell`. The program runs through the user's login shell
(`$SHELL -lc`), so it sees the same `PATH` as in a new terminal tab. `env` entries (`KEY=value`) are
added to its environment; variables that identify the process that started the server, such as Claude
Code's session markers, are removed first.

### Server to dashboard

| op | fields | when |
|----|--------|------|
| `hello` | `version` | first reply to `attach`: the server's protocol version |
| `panes` | `panes` | on attach and whenever the list or a pane's title, size or exit status changes |
| `screen` | `screen` | on attach and whenever a pane's screen changes, at most about 30 times a second |
| `spawned` | `id` | reply to `spawn` |
| `history` | `screen` | reply to `history`: rows from the scrollback (up to 5000 lines) followed by the screen, with `back`, the request clamped to what the scrollback holds, and `maxBack`, how much it holds |
| `error` | `err` | reply to a request that failed |
| `select` | `id` | another connection sent `select` |

A `screen` carries the visible rows only. Each row is a string exactly `cols` cells wide with SGR
escape sequences, colors already mapped to cockpit's palette, so the dashboard can paste it into a card
as is. `cx`/`cy` is the cursor, and `cursor` says whether the program wants it shown.

## Versions

The protocol has a version, `ProtocolVersion` in `proto.go`, raised whenever a message is added. A server
ignores requests it does not know, so a dashboard compares the server's `hello` with its own version:
when the server is older, the dashboard sends `shutdown` as soon as no pane's program is running and
connects again, which starts the newer server. Until then its footer says why. Version 3 added
`history` and version 4 `wheel`; a dashboard sends arrow keys for the wheel to a server older than 4. Version
5 added two pane fields: `fg`, the command line of the job in front of a shell pane when it is not the
shell itself (read from the terminal's foreground process group every 3 seconds), and `lastIO`, when the
pane last took input or printed anything, kept to within 30 seconds so a busy pane does not resend the
list on every write. A server older than 5 sends neither, and no pane is offered the rail. Servers from before
version 1 send no `hello` and do not know `shutdown`; the footer says to restart them by hand.

## Flow control

The server never blocks on a slow dashboard. Each connection has a queue; when it is full, new `screen`
messages are dropped, which is harmless because the next one is a full snapshot. A dropped `panes`
message is sent again on the next tick.

## Example

```json
→ {"op":"attach"}
← {"op":"panes","panes":[]}
→ {"op":"spawn","agent":"shell","cwd":"/Users/me/src","cols":80,"rows":24}
← {"op":"spawned","id":"1"}
← {"op":"panes","panes":[{"id":"1","agent":"shell","cwd":"/Users/me/src","pid":4242,"cols":80,"rows":24,"started":"2026-11-23T14:02:11Z"}]}
← {"op":"screen","screen":{"id":"1","lines":["…"],"cx":2,"cy":0,"cursor":true}}
→ {"op":"input","id":"1","data":"bHMK"}
```
