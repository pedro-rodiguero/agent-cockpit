# Contributing

Thanks for helping. Bug reports with the output of `cockpit json` (with paths and names redacted as you
see fit) and the version of the agent involved are the most useful thing you can send.

## Development

```sh
make build    # bin/cockpit
make test     # go test -race ./...
make lint     # gofmt, go vet, staticcheck
make menubar  # bin/AgentCockpit
```

Run the dashboard from the tree with `go run ./cmd/cockpit`. It starts a pane server from the same
binary; stop it with `pkill -f "cockpit serve"` after changing server code, since a running server is
reused.

Some tests drive real programs and are opt-in through environment variables:

| variable | test |
|----------|------|
| `COCKPIT_RENDER=out.ans` | renders the dashboard against your live sessions into a file |
| `COCKPIT_CHAT=claude:/path/to/session.jsonl` | prints what the chat parser makes of a transcript |
| `COCKPIT_ADOPT_PID=<pid>` | moves that session into a pane; use a disposable session |

## Changes

- Keep changes focused; one pull request per fix or feature.
- Add or update tests for behavior you change. Parsers get a fixture with the smallest log that shows
  the case.
- When you depend on another tool's files or behavior, record it in
  [docs/compatibility.md](docs/compatibility.md).
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat(tui): …`, `fix(source): …`) with the scope set to the package.
- Code comments explain why, not what. Exported identifiers have doc comments.

## Adding an agent

See [docs/sources.md](docs/sources.md#built-in-sources).
