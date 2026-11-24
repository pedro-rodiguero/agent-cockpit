# Sources

A source finds sessions and reports their state. cockpit collects from every enabled source every two
seconds (`-interval`), in parallel, with a four-second timeout per source, and merges the results.

There are two kinds:

- **Built-in sources** (`internal/source`) know a coding agent's own files: Claude Code, Codex, agy.
- **External sources** are programs you install; cockpit runs them and reads their output. Use one for
  anything that is not a coding agent running on this machine.

## External sources

Any executable named `cockpit-source-<name>` in a `PATH` directory is a source called `<name>`. When two
directories hold the same name, the one earlier on `PATH` wins.

### Contract

cockpit runs the program with no arguments and no input, and reads its standard output:

- **Success:** print a JSON array of sessions and exit 0. An empty array means nothing to show.
- **Failure:** exit non-zero. The first line of standard error is shown in the dashboard's footer, so
  keep it short ("offline", "token expired").
- The program must finish within four seconds.

cockpit runs a source at most once every 15 seconds and shows the last good result in between. After a
failure it keeps showing that result for up to 10 minutes, so a backend that is briefly unreachable does
not make the cards blink in and out.

### Session fields

Every field is optional. The full list, with comments, is `model.Session` in
[`internal/model/model.go`](../internal/model/model.go). The ones that matter for items:

| field | default | meaning |
|-------|---------|---------|
| `id` | its position | stable identifier; cockpit prefixes it with `<name>:` |
| `agent` | `<name>` | label shown on the card |
| `name` | | who or what the item is about; the card's title |
| `state` | `inbox` | `inbox`, or an agent state (`waiting`, `asked`, `busy`, `idle`) for sessions running elsewhere |
| `detail` | | one or two words on why it is here: `question`, `review requested`, `alert high` |
| `title` | | the subject |
| `lastPrompt` | | the body; wrapped on the card |
| `since` | | RFC 3339 time the item arrived; used for ordering and age |
| `url` | | opened when the person presses `⏎` on the card |
| `quiet` | `false` | `true` keeps the menu bar app from notifying, for backends that already notify |

### Example

A source that shows open pull requests waiting for your review, using the GitHub CLI:

```sh
#!/bin/sh
# ~/.local/bin/cockpit-source-reviews
gh search prs --review-requested=@me --state=open \
  --json number,title,repository,url,updatedAt --limit 20 |
jq '[.[] | {
  id: "\(.repository.nameWithOwner)#\(.number)",
  name: .repository.name,
  detail: "review requested",
  title: .title,
  since: .updatedAt,
  url: .url
}]'
```

```sh
chmod +x ~/.local/bin/cockpit-source-reviews
COCKPIT_SOURCES=reviews cockpit json   # check the output on its own
```

## Built-in sources

A built-in source implements `source.Source`:

```go
type Source interface {
	Name() string
	Collect(ctx context.Context, env *Env) ([]model.Session, error)
}
```

`env.Procs` is the process table, read once per pass and shared by all sources, so a source never runs
`ps` itself. To add one:

1. Write `internal/source/<agent>.go`. Find live processes in `env.Procs`, read whatever state the
   agent writes, and return one `model.Session` per running session. Set `PID` and `TTY` so `⏎` can
   focus the tab, and `Log` (with a parser in `internal/chat`) if the agent keeps a readable transcript.
2. Register it in `Default()` in `internal/source/source.go`.
3. If the agent can resume a session by id, add it to `resumeSpec` in `internal/tui/app.go` and to
   `mux.Agents`, so `n` and `⏎` (continue here) work for it.
4. Add tests with a small fixture of the agent's files, and list what you depend on in
   [compatibility.md](compatibility.md).

Collect runs every two seconds: cache anything you parse by file size and modification time, as the
Claude and Codex sources do, and keep it to reads of local files.
