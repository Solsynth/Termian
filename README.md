# Termian

The terminal client for [Solian](https://solian.app): a Charm-based TUI and a
scriptable CLI over one authentication core. Termian signs in through Stargate,
either with the RFC 8628 device flow or with a password/TOTP challenge, then
keeps the session alive by rotating the refresh token.

This slice covers authentication and the session shell only. Feeds, posts and
chat are separate work.

## Install

```sh
go install src.solsynth.dev/sosys/termian@latest
```

Or build from a checkout (Go 1.26+):

```sh
go build -o termian .
```

## Quick start

```sh
termian            # interactive client: device login, then the session shell
termian login      # same thing, explicitly
```

The device flow shows a short code and a verification URL. Approve it in a
browser (the Flutter app or <https://id.solian.app/auth/device>), and Termian
moves to the session shell.

## CLI reference

| Command | What it does |
|---|---|
| `termian` | Opens the TUI (device login, or the session shell when credentials still work) |
| `termian login` | Sign in; `--password` selects the challenge flow instead of the device flow |
| `termian logout` | Revokes the session server-side and deletes local credentials |
| `termian whoami` | Prints the account and session |
| `termian refresh` | Rotates the access token and prints the new expiry |
| `termian version` | Prints the build version |

Flow selection:

- The TUI starts when `--no-tui` is absent, `--output` is `text`, and stdout is a
  terminal. Every other combination runs headless, so `termian --output json` is
  a fully scriptable login.
- `--device` (default) uses the device flow; `--password` uses the challenge flow
  and prompts for the account, the factor and the secret on stdin. Without a TTY
  the secret is read as a plain line, which keeps CI/scripted use possible.

Persistent flags:

| Flag | Default | Meaning |
|---|---|---|
| `--server` | `https://api.solian.app` | Stargate API base; `/stargate` is appended when the URL has no path |
| `--client-id` | `solian` | OAuth client used by the device flow |
| `--output` | `text` | `text` or `json` |
| `--store` | `auto` | `auto`, `keyring` or `file` |
| `--config` | XDG config path | Explicit `config.toml` |
| `--device-name` | hostname | Device name reported to the server |
| `--timeout` | `15s` | Per-request timeout |
| `--verbose` | off | Extra diagnostics on stderr |
| `--no-tui` | off | Never start the interactive UI |
| `--device` / `--password` | `--device` | Login flow |

## JSON output

With `--output json`, every record is one compact line on stdout, and errors are
one line on stderr. Each record carries `type`:

| Command | Records |
|---|---|
| `login` (device) | `{"type":"authorization","user_code","verification_uri","verification_uri_complete","expires_at","interval_seconds","server"}`, then `{"type":"authenticated",...}` once approved |
| `login --password` | `{"type":"authenticated","account","session","server","storage","method","access_expires_at"}` |
| `whoami` | `{"type":"account","account","session","server","storage"}` |
| `refresh` | `{"type":"refreshed","access_expires_at","refresh_expires_at","server","storage"}` |
| `logout` | `{"type":"logged_out","server","revoked"}` |
| any failure | `{"type":"error","code","message","status","hint"}` on **stderr** |

Exit codes:

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | other API failure (e.g. `AUTH_ACCOUNT_NOT_FOUND`, `AUTH_INVALID_PASSWORD`) |
| 2 | usage error |
| 3 | not signed in, or the session expired |
| 4 | transport failure (dial, timeout, TLS) |
| 5 | server error or malformed response |
| 6 | rate limited |
| 7 | authorization denied or device code expired |

**Termian never prints tokens**, in either output mode: access tokens live in
memory, and the refresh token only exists in the keyring or the credential file.

## Configuration

`$XDG_CONFIG_HOME/termian/config.toml`, all keys optional:

```toml
server    = "https://api.solian.app"
client_id = "solian"
scope     = "*"
store     = "auto"      # auto | keyring | file
output    = "text"
```

Precedence: flag > environment > file > defaults.

| Variable | Overrides |
|---|---|
| `TERMIAN_SERVER` | `server` |
| `TERMIAN_CLIENT_ID` | `client_id` |
| `TERMIAN_OUTPUT` | `output` |
| `TERMIAN_STORE` | `store` |
| `TERMIAN_CONFIG` | config file location |

A missing config file is fine; a malformed one is a hard error naming the path.

## Storage and security

- The refresh token goes to the system keyring (Secret Service on Linux, Keychain
  on macOS, Credential Manager on Windows) under the service name `termian`, keyed
  by the SHA-256 of the normalized server URL.
- When no keyring is available, `--store auto` falls back to
  `$XDG_STATE_HOME/termian/credentials-<hash>.json`, written atomically with mode
  `0600` inside a `0700` directory, and prints a one-line notice (text mode only).
  `--store keyring` refuses to fall back; `--store file` skips the keyring.
- Access tokens are never persisted.
- The device identity is a SHA-256 of `/etc/machine-id` when present, otherwise a
  random id persisted at `$XDG_STATE_HOME/termian/device-id` (`0600`). A stable
  identity matters: Stargate keys challenge reuse and risky-IP accounting on it.

## Device login notes

The device flow needs a pre-registered OAuth client. Termian defaults to the
public `solian` client, whose allow-list is `*`; override with `--client-id` if you
register your own. The requested scope defaults to `*` and is configurable through
`scope` in `config.toml`, because `openid`, `profile` and `email` are rejected as
`invalid_scope` for that client.

## Architecture

```
main.go                     entry point
internal/api                Stargate client: device flow, challenge flow, sessions
internal/api/tokensource.go access-token rotation, single-flight, session expiry
internal/credentials        keyring store with 0600 file fallback
internal/deviceid           stable device identity
internal/config             config file + env + flags
internal/ui                 text/NDJSON renderer and exit codes
internal/cli                cobra commands and the headless flows
internal/tui                Bubble Tea app: login, password, session shell
```

The TUI talks to an `API` interface (`internal/tui/app.go`), which `*api.Client`
satisfies, so the screens are testable without a server.

## Development

```sh
go build ./... && go vet ./... && go test ./...
```

The test suite is offline: HTTP is served by `httptest`, the keyring by
`keyring.MockInit*`, and the TUI is driven by `teatest`.
