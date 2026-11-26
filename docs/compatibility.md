# Compatibility

cockpit reads files and sockets that belong to other tools. Most of them are internal to those tools,
not documented interfaces, so a new release can change them. This page lists exactly what cockpit relies
on, so a breakage can be traced quickly. Last checked with Claude Code 2.1.330, Codex CLI 0.167.1 and
Antigravity CLI 1.5.10, on macOS 15. Linux is built and tested in CI.

## Claude Code

| what | used for |
|------|----------|
| `<config>/sessions/<pid>.json`: `pid`, `sessionId`, `cwd`, `name`, `kind`, `status` (`busy`, `waiting`, `idle`), `waitingFor`, `startedAt`, `updatedAt`, `statusUpdatedAt`, `messagingSocketPath` | discovery and state |
| `<config>/projects/*/<sessionId>.jsonl`: `ai-title` and `last-prompt` records, and `user`/`assistant` messages with `text` and `tool_use` blocks | card title, last prompt, chat |
| in the same transcript: `message.model`, `message.usage` (input and cache token counts), `gitBranch`, `effort`, `isSidechain`, `permission-mode` records, and `tool_use` ids matched against `tool_result` blocks | model, context size, branch, effort, permission mode, the tool call in progress |
| in the same transcript: `slug` on `user`/`assistant` records, naming the plan file `<config>/plans/<slug>.md`, or `<plansDirectory>/<slug>.md` when the project's local, project or the profile's user settings set `plansDirectory` (relative to the project; outside it is ignored, as Claude Code does) | `m` (plan viewer) |
| `claude --resume <sessionId>` | moving a session into a pane |
| the messaging socket: newline-delimited JSON, an optional `{"type":"auth","token"}` frame using the token in `<config>/sessions/<pid>.<sha256(socket path)>.key`, then a `user` frame wrapped in `<cross-session-message>` | `r` (reply) |

Claude Code writes the session file only for interactive sessions that are not children of another
session. That is why the pane server removes `CLAUDECODE` and `CLAUDE_CODE_*` from the environment it
passes to panes: a server started from inside a Claude Code session would otherwise pass them on, and
the agents in its panes would never register.

## Codex CLI

| what | used for |
|------|----------|
| `~/.codex/sessions/**/rollout-<time>-<uuid>.jsonl`, held open by the `codex` process | discovery; the uuid is the session id |
| first line `session_meta` with `payload.cwd` | matching a rollout to a process when it is not held open |
| `event_msg` records: `task_started`, `task_complete` (`last_agent_message`), `turn_aborted`, approval and `request_user_input` events, `item_completed` items (`UserMessage`, `AgentMessage`, `CommandExecution`, `FileChange`, `McpToolCall`, `ImageView`) | state and chat |
| `turn_context` records (`model`) and `token_count` events (`last_token_usage.total_tokens`, `model_context_window`) | model and context size |
| `codex resume <uuid>` | moving a session into a pane |

## Antigravity CLI (agy)

| what | used for |
|------|----------|
| `~/.gemini/antigravity-cli/conversations/<id>.db`, held open by the `agy` process | discovery and a guess at the state from its modification time |

agy stores conversations in SQLite with protobuf payloads, so cockpit shows no chat for it and cannot
tell when it is waiting for input.

## Operating system

- `ps` for processes and their terminals, on both systems.
- macOS: `lsof` for working directories and open files.
- Linux: `/proc/<pid>/cwd` and `/proc/<pid>/fd` for the same, and `xdg-open` for links.
- AppleScript against Terminal.app (`tty`, `selected`, `custom title` of tabs) and iTerm2 (`tty`,
  `name` of sessions).
