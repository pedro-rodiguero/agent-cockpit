# Security

The to-do server (`cockpit-todo-server`, [todo-server.md](todo-server.md)) is the part of cockpit that
faces the internet, so it carries a security routine that runs on every push. The dashboard and the pane
server only listen on the user's own Unix socket.

## What runs, and where

| layer | where | what it checks |
|-------|-------|----------------|
| OWASP Top 10 suite | `internal/todoserver/*_test.go`, in `make test` | every route in the route table without credentials, with the wrong kind and against the docs; lockout, per-IP limit with a forged `X-Forwarded-For`, session rotation and expiry, CSRF, password change, 2FA with replay, recovery codes, passkeys (single-use challenges bound to their ceremony and session, user verification required, counters, lock), sessions, devices; path traversal with a planted bait file; stored XSS payloads; a static check that the web app never uses `innerHTML`, `eval` or inline code; that the server imports nothing that runs commands; body limits, deep JSON, every JSON type in every field never giving a 500; security headers; logs, audit log and state file holding no password, token or code |
| Sign-in primitives | `internal/auth/*_test.go` | scrypt parameters and their bounds, the password policy, TOTP against RFC 6238's vectors, sealing, the limiter, the client IP, WebAuthn registration and assertion against a software authenticator (`authtest`) |
| Fuzzing | `go test -fuzz` in `security.yml` | `todo.Parse`, `List.Merge`, stored hashes, TOTP codes, the CBOR reader behind passkeys; seeds run in every `go test` |
| Known vulnerabilities | `govulncheck` in `security.yml`, also weekly | the Go dependencies and the standard library |
| Static analysis | Semgrep in `security.yml` | `p/owasp-top-ten`, `p/golang`, `p/javascript`, `p/secrets`; ERROR fails |
| Secrets | gitleaks over the whole history | |
| Passive scan | ZAP baseline against a server started in the job | `.zap/rules.tsv` says which alerts fail the job and why the others are warnings |
| Dependencies | Dependabot, weekly, with a week's cooldown | Go modules and the workflow actions |

Actions are pinned by commit SHA and the tool images by digest; Dependabot updates the actions, and the
images are updated by hand, version and digest together.

## The local side

The pane server's socket accepts "run this command", so reaching it means running anything as the user.

- **Socket.** It lives in a directory only the user can enter (`0700`, the socket `0600`), under the user
  cache directory, or a per-user folder under the temporary directory when that path is too long. The
  server and the client both refuse a directory that is a link, belongs to someone else or others can
  enter, so nobody can make the folder first under a shared `/tmp` and receive the keystrokes; the server
  also refuses any connection from another user (`LOCAL_PEERCRED` on macOS, `SO_PEERCRED` on Linux).
  Tests: `internal/mux/private_test.go`.
- **Pastes.** Pasted text reaches a pane with every ESC and bracketed-paste end marker taken out, so it
  can neither end the paste early nor send keys (`internal/mux/paste_test.go`); a file dropped on the
  menubar terminal loses its control characters before its path is typed.
- **Agent text.** The plan viewer strips every control character and escape sequence from the Markdown
  before laying it out (`internal/tui/markdown_test.go`); agent output never reaches the outer terminal
  as sequences, only as the cells the emulator drew.
- **What a pane can do.** Agents start through the user's login shell with `exec`, so no shell is left
  under them when they exit; a pane has exactly the rights of the user who runs cockpit.

**Regression rule.** A flaw found by any means (the suite, fuzzing, Semgrep, ZAP, a report) first becomes a
test that fails, and only then is fixed. A fuzzing failure leaves its input in `testdata/fuzz/`; committed,
it becomes a regular test case.

## Known gaps

Decided on 2026-11-24 for a server with one user:

- **No e-mail.** There is no "forgot password" by e-mail, no invitation and no e-mail change: the account
  is created and its password reset with `cockpit-todo-server set-password` on the server, which ends
  every session and is audited. Whoever can run it already controls the server.
- **No staging environment**, so no active scan (ZAP full scan, Burp) against the live server: run those
  against a local instance with throwaway data.
- **No external monitoring yet** (certificate transparency, DNS, exposure): to add once the server has
  been live for a while.
- **No manual pentest**: nobody but its owner uses the app.

The ZAP baseline scan in `.github/workflows/security.yml` reads `.zap/rules.tsv`, which says which of its
rules fail the build, which only warn (each with its reason) and which are ignored.

## Reporting a vulnerability

Open a private security advisory on the repository (Security › Report a vulnerability) rather than an
issue.
