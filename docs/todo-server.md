# To-do server

`cockpit-todo-server` keeps a copy of the to-do list on a server of your own, so a phone can read and
change it: it serves a web app you add to the home screen, and the computers running cockpit sync their
`todo.json` with it. One person, one list.

```
iPhone (web app) ──HTTPS──┐
                          ├── cockpit-todo-server ── todo.json, auth.json, audit.jsonl
Mac (cockpit) ──HTTPS─────┘        behind Caddy (TLS)
```

## Running it

```sh
cockpit-todo-server set-password            # create the account, or reset its password (asks twice)
cockpit-todo-server serve                   # listen on 127.0.0.1:8740 behind a reverse proxy
```

| variable | default | |
|----------|---------|---|
| `TODO_DATA` | `/var/lib/cockpit-todo` | where the list, the account and the audit log live |
| `TODO_LISTEN` | `127.0.0.1:8740` | |
| `TODO_ORIGIN` | required | the public origin, `https://todo.example.com`: writes from any other origin are refused |
| `TODO_ENC_KEY` | required for 2FA | 32 bytes, hex or base64 (`openssl rand -hex 32`): encrypts the 2FA secret. Back it up with the data |
| `TODO_TRUST_PROXY` | `none` | `caddy` when Caddy on the same machine forwards to it: the client IP is then the last `X-Forwarded-For` hop, and only for connections from loopback |

`set-password` and `reset-2fa` refuse to run while the server answers on `TODO_LISTEN`: it keeps the
account in memory and would undo the change. Stop it, run them, start it again.

There is no e-mail: the account is created, and its password reset, with `set-password` on the server.
Resetting the password ends every session and is written to the audit log. Whoever can run it already
controls the server.

## Security

The sign-in follows OWASP ASVS level 2: scrypt password hashes (N=2^17), a password of 12 to 128
characters not on a list of common ones, a progressive lockout after 5 wrong attempts (15 minutes,
doubling up to a day) plus a per-IP limit, server-side sessions in a `__Host-` cookie that expire after
30 minutes idle or 12 hours, CSRF tokens on every write, optional TOTP 2FA with recovery codes, and an
append-only audit log. Computers sync with a device token, created and revoked in the app; the server
keeps only its hash.

## API

JSON in both directions. Errors are `{"error": "message"}` with a 4xx status; nothing returns a stack or
a path.

Every request that changes something (`POST`, `PATCH`, `DELETE`) made with the session cookie must send
`Content-Type: application/json`, `X-CSRF-Token: <csrf>` and an `Origin` equal to `TODO_ORIGIN`.
`csrf` comes from the sign-in response and from `GET /api/me`. A write that fails these checks gets 403
(415 for the wrong content type); a request without a live session gets 401, which means signing in
again. Requests without fields send `{}`; `DELETE` sends no body.

### Signing in

| request | response |
|---------|----------|
| `POST /api/login` `{"password"}` | `{"next": "done", "csrf"}` and the session cookie, or `{"next": "mfa"}` and a 5-minute challenge cookie. 401 wrong password, 423 locked (says until when, only after the right password), 429 too many attempts from this IP |
| `POST /api/login/mfa` `{"code"}` | a 6-digit code or a recovery code: `{"next": "done", "csrf"}` and the session cookie |
| `POST /api/logout` | 204 |
| `GET /api/me` | `{"csrf", "mfa": {"on", "pending", "recoveryLeft"}}`, or 401 when signed out |

The login and MFA requests need `Content-Type` and `Origin` but no CSRF token (there is no session yet).

### Tasks

A task is the list's own format (`internal/todo`):

```json
{"id": "t1a2b3c4d5e6", "text": "call the venue", "group": "Personal", "due": "2026-11-25",
 "created": "2026-11-24T10:00:00Z", "updated": "2026-11-24T10:00:00Z", "done": "2026-11-24T12:00:00Z"}
```

`group` and `due` may be missing (the default group; no date), `done` is missing while the task is open.

| request | response |
|---------|----------|
| `GET /api/tasks` | `{"tasks": [...], "groups": [...], "defaultGroup"}`: open tasks and those completed in the last week, in the dashboard's order (by group, then due date). `groups` are the named groups in use, in order, without the default one |
| `POST /api/tasks` `{"line"}` | 201 `{"task"}`. `line` uses the dashboard's quick-add syntax: `call the venue #Personal @tomorrow` (`@today`, `@tomorrow`, `@25/12`, `@2027-01-31`) |
| `PATCH /api/tasks/{id}` `{"line"}` or `{"done": true\|false}` | `{"task"}`. `line` replaces text, group and due date, as editing in the dashboard does |
| `DELETE /api/tasks/{id}` | 204 |

### Account

| request | response |
|---------|----------|
| `POST /api/password` `{"current", "new"}` | `{"csrf"}`: ends the other sessions and rotates this one (new cookie, new CSRF token). 400 for a weak new password, 403 for a wrong current one, which counts toward the lock (423 once locked, which also ends every session) |
| `GET /api/sessions` | `{"sessions": [{"id", "current", "started", "lastUsed", "ip", "agent"}]}` |
| `DELETE /api/sessions/{id}` | 204 |
| `POST /api/sessions/end-others` | `{"ended": n}` |
| `POST /api/mfa/start` | `{"secret", "uri"}`: an `otpauth://` URI; 2FA is on only after `confirm` |
| `POST /api/mfa/confirm` `{"code"}` | `{"recoveryCodes": [10 codes]}`, shown once |
| `POST /api/mfa/disable` `{"password"}` | 204; ends every session. 403 for a wrong password, as above |
| `GET /api/devices` | `{"devices": [{"id", "name", "created", "lastUsed"}]}` |
| `POST /api/devices` `{"name"}` | 201 `{"device", "token"}`: the token is shown once |
| `DELETE /api/devices/{id}` | 204 |
| `GET /api/audit` | `{"events": [{"at", "event", "ip", "agent", "details"}]}`, the last 100, newest first; `details` is an object of strings |

### Sync (computers)

`POST /api/sync` with `Authorization: Bearer <device token>` and `{"tasks": [...]}`, every task the
computer has, tombstones included (`"deleted": "<time>"`). The server merges them into its list (for each
task the version changed last wins, `todo.List.Merge`) and answers `{"tasks": [...]}`, its whole list
with tombstones, which the computer merges back. No cookie, so no CSRF token.

## The web app

`GET /` serves a web app made to be added to an iPhone's home screen (Safari › Share › Add to Home
Screen). It lists the tasks by group, adds them with the quick-add syntax, completes, reopens, edits and
deletes them, and works offline: it shows the last list it saw and sends what was done offline when the
connection returns. Settings holds the password, 2FA, sessions, devices and the audit log.

There is no way to make new recovery codes besides turning 2FA off and on again. A session ends after
30 minutes without use, and the app then asks to sign in again, keeping the changes not yet sent.
