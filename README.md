# Termian

The terminal client for [Solian](https://solian.app): a Charm-based TUI and a
scriptable CLI over one authentication core. Termian signs in through Stargate,
either with the RFC 8628 device flow or with a password/TOTP challenge, then
keeps the session alive by rotating the refresh token.

Signed in, it browses the public timeline, reads and writes posts, replies,
reactions and bookmarks, and reads and sends chat messages. Signed out, the
public timeline and post details stay readable: press `g` on the login screen to
browse as a guest.

## Install

```sh
go install src.solsynth.dev/sosys/termian@latest
```

Or build from a checkout (Go 1.26+):

```sh
go build -o termian .

# hosted server (separate module; Go 1.26.8+ and shared v0.2.6)
go build -o termian-server ./hosted/cmd/termian-server
```

The client binary is self-contained. The hosted server lives in `hosted/`, has
its own `go.mod`, and is the only part that uses Wish and Stargate's generated
gRPC client, so `go install` and client builds never pull those dependencies.

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
| `termian feed` | Lists the ranked timeline (`--mode`, `--cursor`); works with or without a session |
| `termian post <id>` | Prints a post and its replies |
| `termian publish` | Publishes a status, article or blog link (`--content`, `--file -`, `--type`, `--title`, `--description`, `--publisher`, `--tag`, `--attach`, `--visibility`, `--language`, `--slug`) |
| `termian reply <id>` | Replies to a post |
| `termian react <id>` | Toggles a reaction (`--symbol`, default `heart`) |
| `termian save <id>` | Bookmarks a post; `--remove` drops the bookmark |
| `termian bookmarks` | Lists saved posts |
| `termian publishers` | Lists the publishers you can post as |
| `termian chats` | Lists chat rooms with unread counts |
| `termian chat <room-id>` | Prints a room's messages; `--send --content …` posts one |
| `termian version` | Prints the build version |

`feed` and `post` read public data, so they work before signing in; `feed`
ranks for the account when a session is available. Everything else needs a
session and exits 3 without one.

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
| `--attachments` | `browser` | `browser`, `preview` or `off` |
| `--chat-history` | `200` | Messages kept per chat room |

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
| `feed` | `{"type":"post","post":{...}}` per post, then `{"type":"timeline","mode","next_cursor"}` |
| `bookmarks` | `{"type":"post","post":{...}}` per post |
| `post` | `{"type":"post",...}` then `{"type":"reply","post","reply"}` per reply |
| `publish`, `reply` | `{"type":"created","post":{...}}` |
| `react` | `{"type":"reaction","post","symbol","is_present"}` |
| `save` | `{"type":"bookmark","post","is_bookmarked"}` |
| `publishers` | `{"type":"publisher","id","name","nick","kind"}` |
| `chats` | `{"type":"chat_room","room":{...}}` per room |
| `chat` | `{"type":"chat_message","message":{...}}` oldest first |
| `chat --send` | `{"type":"chat_sent","message":{...}}` |
| any failure | `{"type":"error","code","message","status","hint"}` on **stderr** |

`react` follows the server's toggle semantics: posting a symbol that the account
already reacted with removes the reaction, and `is_present` reports the state
after the call.

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
server       = "https://api.solian.app"
client_id    = "solian"
scope        = "*"
store        = "auto"      # auto | keyring | file
output       = "text"
attachments  = "browser"   # browser | preview | off
chat_history = 200         # messages kept per chat room
```

Precedence: flag > environment > file > defaults.

| Variable | Overrides |
|---|---|
| `TERMIAN_SERVER` | `server` |
| `TERMIAN_CLIENT_ID` | `client_id` |
| `TERMIAN_OUTPUT` | `output` |
| `TERMIAN_STORE` | `store` |
| `TERMIAN_ATTACHMENTS` | `attachments` |
| `TERMIAN_CHAT_HISTORY` | `chat_history` |
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
- Access tokens are never persisted. Hosted sessions keep both tokens in
  per-connection process memory (`credentials.NewMemory`) and write nothing to
  disk or a shared keyring.
- The device identity is a SHA-256 of `/etc/machine-id` when present, otherwise a
  random id persisted at `$XDG_STATE_HOME/termian/device-id` (`0600`). A stable
  identity matters: Stargate keys challenge reuse and risky-IP accounting on it.

## Device login notes

The device flow needs a pre-registered OAuth client. Termian defaults to the
public `solian` client, whose allow-list is `*`; override with `--client-id` if you
register your own. The requested scope defaults to `*` and is configurable through
`scope` in `config.toml`, because `openid`, `profile` and `email` are rejected as
`invalid_scope` for that client.

## Hosted mode

`termian-server` runs the same TUI as an SSH application, one Bubble Tea program
per connection. A user connects with any SSH client; the server validates the
offered key against Stargate and exchanges it for a Solian session, so there is
no device code or password step.

```sh
go build -o termian-server ./hosted/cmd/termian-server
./termian-server \
  --listen :2222 \
  --stargate-grpc 127.0.0.1:9090 \
  --exchange-token "$TERMIAN_EXCHANGE_TOKEN"
```

The server is a separate module. It builds in this checkout through
`hosted/go.work`, which points at `../LightHouse/shared`; container and CI
builds use `GOWORK=off` and fetch the published `shared/v0.2.6` module.

Stargate must enable the exchange with the same secret:

```toml
[hosted]
exchangeToken = "…"
```

The user's public key must be registered on their Solian account through
`POST /api/keys/ssh` (Settings → SSH keys in the console). Keys are public
material, so the exchange RPC is closed unless the deployment configured a
token; the SSH handshake is what proves possession.

Flow:

1. The SSH server asks Stargate's `DyAccountKeyService` to
   `ExchangeSSHPublicKeyForSession` with the offered key, the service token and
   a purpose (default `termian-hosted`).
2. Stargate resolves the key to its account, refuses locked accounts, creates
   an OIDC session and returns access and refresh tokens.
3. The TUI starts on the session shell with those tokens; the refresh token
   stays in per-session memory and rotates through `/auth/token` like a
   challenge login.

Hosted flags:

| Flag | Default | Meaning |
|---|---|---|
| `--listen` | `:2222` | SSH listen address |
| `--stargate-grpc` | `127.0.0.1:9090` | Stargate's internal gRPC address |
| `--stargate-grpc-tls` | off | TLS to Stargate |
| `--stargate-grpc-ca` | empty | CA bundle verifying Stargate; empty skips verification |
| `--stargate-grpc-cert` | empty | client certificate for a Stargate enforcing mTLS |
| `--stargate-grpc-key` | empty | client key paired with `--stargate-grpc-cert` |
| `--stargate-grpc-server-name` | empty | verified certificate name; defaults to the dial host |
| `--exchange-token` | `$TERMIAN_EXCHANGE_TOKEN` | Hosted exchange secret; required |
| `--purpose` | `termian-hosted` | Purpose echoed in Stargate's audit log |
| `--host-key` | `$XDG_STATE_HOME/termian/host_ed25519` | SSH host key; generated when missing |

Fleet deployments enable `--stargate-grpc-tls` and present the shared client
certificate; Stargate's gRPC listener enforces mTLS when `clientCAFile` is set.

`--server`, `--client-id`, `--attachments`, `--chat-history` and `--timeout`
apply to hosted sessions too. The attachment policy defaults to `preview`
because the server has no display for `browser`.

### Containers

`Dockerfile` defines two targets:

| Target | Binary | Use |
|---|---|---|
| `server` (default) | `termian-server` | Hosted fleet image; exposes `2222` |
| `client` | `termian` | Client-only TUI image, no SSH server |

```sh
docker build --target server -t termian-server .
docker build --target client -t termian-client .

docker run --rm -p 2222:2222 \
  -e TERMIAN_EXCHANGE_TOKEN \
  -v termian-host-key:/data \
  termian-server --stargate-grpc stargate:9090 --stargate-grpc-tls

docker run --rm -it -v termian-client-state:/data termian-client
```

Both runtime images are `scratch` with CA certificates and `XDG_STATE_HOME=/data`
so credential and host-key files survive through the volume. The server image
needs `shared/v0.2.6` published before it can compile; the client image does not.

## Architecture

```
main.go                     entry point
internal/api                Stargate client: device flow, challenge flow, sessions
internal/api/content.go     sphere client: timeline, posts, edits, replies, reactions, bookmarks
internal/api/chat.go        messager client: rooms, messages, edits, reactions
internal/api/tokensource.go access-token rotation, single-flight, session expiry
internal/credentials        keyring store with 0600 file fallback and a per-session memory store
internal/deviceid           stable device identity
internal/config             config file + env + flags
internal/ui                 text/NDJSON renderer and exit codes
internal/cli                cobra commands and the headless flows
hosted/                     separate module: wish SSH server and command
internal/tui                Bubble Tea app: login, session shell, feed, post, chats
internal/tui/attach.go      attachment listing, browser hand-off and image previews
```

The TUI talks to two interfaces (`internal/tui/app.go`): `AuthAPI` for the login
panes and `ContentAPI` for the feeds and chats. `*api.Client` satisfies both, so
the screens are testable without a server.

## TUI keys

| Pane | Keys |
|---|---|
| Login | `g` browse as guest, `p` password login, `c` copy code, `o` open browser, `esc` quit |
| Session shell / Profile | `j`/`k` select a publisher or notification topic, `enter` apply, `r` refresh, `f` timeline, `b` bookmarks, `c` chats, `n` new post, `l` logout |
| Notifications | `j`/`k` select, `enter` open the post or link, `a` mark all read, `d` delete, `R` reload, `esc` back |
| Any content pane | `tab` next section, `shift+tab` previous section |
| Feed | `j`/`k` move, `G` last, `enter` open, `r` react, `b` bookmark, `t` timeline/bookmarks, `c` post, `e` edit, `d` delete, `R` reload, `esc` back |
| Post | `j`/`k` move, `enter` open a reply, `r` react, `b` bookmark, `c` reply, `e` edit, `d` delete, `o` open attachment, `p` preview, `R` reload, `esc` back |
| Chats | `j`/`k` move, `enter` step into the conversation, `i` write, `R` reload, `tab` sections, `esc` back |
| Chat room | `j`/`k` move, `k` at the top pages in older history, `g`/`G` jump to either end, `r` react, `i` write, `o` open attachment, `h` back to the room list |
| Composer | `tab`/`shift+tab` fields, `ctrl+d` publish, `ctrl+p` publisher, `ctrl+t` status/article/blog, `ctrl+v` visibility, `ctrl+o` load local markdown as an article, `ctrl+a` attach a file, `alt+enter` newline, `esc` cancel |
| Confirmation | `y` confirm, `n`/`esc` cancel |

Write keys are refused locally when browsing as a guest: the status line asks for
a sign-in instead of failing at the server. Deleting a post asks for
confirmation first.

## Attachments

`attachments` decides what Termian does with a post's files:

| Mode | Behaviour |
|---|---|
| `browser` (default) | Lists name, type and size; `o` hands the file to the system opener |
| `preview` | Also renders the first image inline in the post pane; `p` re-downloads it |
| `off` | Hides attachments entirely |

Previews download at most 8 MiB per file and render into 40×10 cells: half
blocks in true color terminals, a luminance ASCII ramp otherwise. Rendered
previews are cached for the session.

## Sections

The browsing panes share one workspace, switched with `tab`: **timeline**,
**chats**, **bookmarks**. The pane header names the active list.

The signed-in shell also exposes **notifications** (`3`) and a **profile/settings**
page (`4`). Notifications mirror Ring: topic, title, body, action link and an
unread dot, with `a` to mark everything read and `d` to delete. `enter` follows
the action link: post links open the post pane, everything else goes to the
system browser.

The profile page shows the account id, role, perks, locale, activation date,
level/XP, publishers, notification topics and the live session card. `enter` on
a publisher makes it the composer default; `enter` on a notification topic
cycles `normal → silent → reject` and writes the preference to Ring.

Chat is an IRC-style workspace in three columns: rooms on the left, the
transcript in the middle as `time <nick> text` lines, and the room's nicklist on
the right. The active column is labelled `rooms ◂` or `conversation ◂` and the
header names the focus, so `j`/`k` scope is explicit. Rooms with unread messages
carry a `[*]` marker, and members show `●` (online), `◐` (idle) or `○`
(offline). The nicklist is hidden on narrow terminals and never shows the
signed-in account.

Chat scrollback pages backwards through the room history 50 messages at a time
and keeps at most `chat_history` messages per room. System events (call presence,
attachment finalisation, reaction receipts) are filtered out of the transcript,
so the offset tracks rows read from the server rather than visible messages.

## Realtime and composer

When a session exists the TUI opens the Solian websocket gateway
(`/ws?namespace=dev.solsynth.solian`) with the bearer token. `notifications.new`
and `messages.new` frames update the badge, the notification list and the open
chat immediately; each frame with an `_ack_id` is acknowledged, new connections
request `session.resume`, and the client reconnects with backoff. The footer
shows `● live` while the socket is open.

The composer supports three post types: status, article and blog link. `ctrl+o`
loads a local Markdown file as an article and fills the title from the first
heading (and the description from the first paragraph); `ctrl+a` uploads a local
file to Drive and attaches it (20 MiB direct-upload limit); `ctrl+p` cycles
publishers; `ctrl+t` cycles the type; `ctrl+v` cycles visibility. Titles,
descriptions and comma-separated tags are part of the publish and edit payloads.
Blog links must be absolute `http(s)` URLs.

## Development

```sh
go build ./... && go vet ./... && go test ./...
cd hosted && go build ./... && go vet ./... && go test ./...
```

The test suite is offline: HTTP is served by `httptest`, the keyring by
`keyring.MockInit*`, and the TUI is driven by `teatest`.
