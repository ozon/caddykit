# caddykit

A single Go binary that manages selected Caddy proxy routes for local
developer services — via the Caddy Admin API, with a CLI and web UI,
a local desired state, undo, and safe recovery.

No Caddyfile file access and no applying of Caddyfiles, but a read-only
Caddyfile preview; no systemd, no global Caddy settings, no multi-user
operation. See [Product limits](#product-limits).

## Requirements

| Area | Requirement |
|---|---|
| Caddy | **≥ 2.8.0 recommended** (Etag as a response header); 2.6/2.7 with Etag as a trailer run in compatibility mode (tested: 2.10/2.11) |
| Runtime | Linux amd64 or arm64 |
| Build | Go ≥ 1.23, Node ≥ 22.12, npm |
| Integration tests | Docker (Caddy 2.10 and 2.11) |

`caddykit` talks exclusively to the Caddy Admin API. Caddy must already be
running and serving the desired domain over HTTPS; certificates and the
global Caddy configuration are never touched.

## Building and testing

```sh
make build        # frontend (Svelte) + Go binary for amd64 and arm64 -> dist/
make test         # Go unit tests
make lint         # go vet, svelte-check, eslint (+ golangci-lint if installed)
make integration  # real Caddy containers: 2.10 and 2.11
```

`make build` produces:

```
dist/caddykit-linux-amd64
dist/caddykit-linux-arm64
```

The web UI is embedded in the binary (`embed`); at runtime neither Node nor a
separate frontend is required.

## Quick start

```sh
./dist/caddykit-linux-amd64 web --domain proxy.dev.example.com
```

On startup:

1. exclusive instance lock,
2. resolve any open OpJournal,
3. check Caddy reachability and Etag,
4. generate its own startup password,
5. check for domain conflicts,
6. create/repair `caddykit/self`,
7. bind the backend to `127.0.0.1:<ephemeral>`,
8. print the URL and password **once** to stdout,
9. initial reconcile, then every 10 s (±20 % jitter).

```
https://proxy.dev.example.com
Startup password: XXXX…
```

The UI runs on the given domain through Caddy. There is no HTTP fallback.
On shutdown, `caddykit/self` is switched to a static 503 response; the
developer service routes remain active.

## Configuration

Priority: **CLI argument > environment variable > config file > default**.

| Parameter | Argument | Env | Default |
|---|---|---|---|
| Caddy Admin API | `--caddy-admin` | `CADDYKIT_CADDY_ADMIN` | `http://localhost:2019` |
| Web UI domain | `--domain` | `CADDYKIT_DOMAIN` | — (required for `web`) |
| Data directory | `--data-dir` | `CADDYKIT_DATA_DIR` | `$XDG_DATA_HOME/caddykit` |
| Template directory | `--template-dir` | `CADDYKIT_TEMPLATE_DIR` | `$XDG_CONFIG_HOME/caddykit/templates` |
| Reconcile interval | `--reconcile-interval` | `CADDYKIT_RECONCILE_INTERVAL` | `10s` (2s–5m) |
| HTTP server | `--server` | `CADDYKIT_SERVER` | Autodetect (`:80`/`:443`, exactly one candidate) |
| Remote admin | `--allow-remote-admin` | — | off; HTTPS or Unix socket only |
| Insecure upstream TLS | `--allow-insecure` | — | off |
| Log level | `--log-level` | `CADDYKIT_LOG_LEVEL` | `info` for `web`, `off` for all other commands |
| Log file | `--log-file` | `CADDYKIT_LOG_FILE` | stderr |

Remote admin endpoints are rejected without `--allow-remote-admin`; with the
flag, only HTTPS or a Unix socket is allowed. Unix sockets use the
Caddy notation `unix//run/caddy/admin.sock`.

`--log-level` accepts `off`, `error`, `warn`, `info`, and `debug`. With
`--log-file`, caddykit writes the lines to a file instead (0600, append);
without the flag they go to stderr. A disabled log level does not create a log
file. The default `off` for one-shot commands keeps their output unchanged.

### Settings file

Optional, hand-edited YAML at `$XDG_CONFIG_HOME/caddykit/config.yaml`
(usually `~/.config/caddykit/config.yaml`). It is read at startup only; a
change requires a restart. A missing or empty file means defaults.

```yaml
domain: proxy.dev.example.com
caddy_admin: http://localhost:2019
data_dir: /home/me/.local/share/caddykit
template_dir: /home/me/.config/caddykit/templates
reconcile_interval: 15s
server: srv0
log_level: info
log_file: /home/me/.local/share/caddykit/caddykit.log
```

Precedence: **CLI argument > environment variable > config file > default**.

- Keys mirror the flags; every key is optional.
- `domain` applies to `web` only, exactly like `CADDYKIT_DOMAIN`.
- `allow_remote_admin` and `allow_insecure` are deliberately **not** supported
  here; those stay explicit CLI flags.
- Unknown keys, invalid YAML, and invalid values (e.g. an unparseable
  `reconcile_interval`) abort startup with exit code 2 and name file and key.
  An out-of-range interval is only rejected by `web`, since route commands do
  not use it.
- `caddykit web` does not write this file; the Settings page shows runtime
  state only.

### Environment variables

Priority: **CLI argument > environment variable > config file > default**.
Variables are read only at process start; a change requires a restart. Empty
variables are treated as unset.

#### By caddykit itself

| Variable | Read by | Meaning | Default / fallback |
|---|---|---|---|
| `CADDYKIT_CADDY_ADMIN` | `web`, CLI | Caddy Admin API; `http(s)://…` or `unix//path/to.sock` | `http://localhost:2019` |
| `CADDYKIT_DOMAIN` | `web` | Public UI domain; required for `web` | — (`--domain` then required) |
| `CADDYKIT_DATA_DIR` | `web`, CLI | State, undo, journal, locks, IPC token, socket fallback | `$XDG_DATA_HOME/caddykit`, otherwise `$HOME/.local/share/caddykit` |
| `CADDYKIT_TEMPLATE_DIR` | `web`, CLI | Local templates (take precedence over embedded ones) | `$XDG_CONFIG_HOME/caddykit/templates`, otherwise `$HOME/.config/caddykit/templates` |
| `CADDYKIT_RECONCILE_INTERVAL` | `web` | Reconcile interval as a Go duration (`10s`, `2m`, `1h30m`) | `10s`; valid 2s–5m |
| `CADDYKIT_SERVER` | `web`, CLI | Name of the Caddy HTTP server | Autodetect (`:80`/`:443`, exactly one candidate); the one-shot CLI otherwise uses the server stored in `state.json` |
| `XDG_DATA_HOME` | `web`, CLI | Base for `CADDYKIT_DATA_DIR` if that is unset | `$HOME/.local/share` |
| `XDG_CONFIG_HOME` | `web`, CLI | Base for `CADDYKIT_TEMPLATE_DIR` if that is unset | `$HOME/.config` |
| `XDG_RUNTIME_DIR` | `web`, CLI | Directory for the IPC socket; socket file mode 0600 | Data directory if unset or not a directory |
| `HOME` | `web`, CLI | Fallback for the XDG paths | —; if `HOME` is missing, relative paths (`.local/share/caddykit`, `.config/caddykit/templates`) are used |
| `EDITOR` | CLI | Editor for `routes edit --json-edit` | `vi` |
| `TMPDIR` | CLI | Temporary file for the JSON editor (mode 0600, deleted afterwards) | Go default, usually `/tmp` |
| `PATH` | CLI | Resolution of the editor command from `$EDITOR` | Process PATH |
| `SSL_CERT_FILE` | `web`, CLI | Additional CA file for **remote** HTTPS admin endpoints (Go/x509) | System trust store |
| `SSL_CERT_DIR` | `web`, CLI | Additional CA directory for remote HTTPS admin endpoints | System trust store |

Details and pitfalls:

- **`CADDYKIT_DOMAIN` only applies to `web`.** For `routes …`, `--domain` is
  the route's hostname; these commands ignore `CADDYKIT_DOMAIN`.
- **An invalid `CADDYKIT_RECONCILE_INTERVAL`** (e.g. without a unit) is
  silently ignored; the default `10s` applies. A syntactically valid value
  outside 2s–5m causes a startup error (exit 2).
- **Paths are not expanded.** `~` is not resolved; relative paths refer to the
  process's working directory. The data directory is created with mode 0700.
  The runtime directory must exist and be writable; otherwise the socket is
  created in the data directory.
- **`CADDYKIT_TEMPLATE_DIR` does not need to exist.** If it is missing or
  empty, only the embedded default templates (`mcphub`, `omniroute`) are used.
  Local YAML files with the same ID take precedence.
- **`XDG_RUNTIME_DIR` must be identical for the web process and the CLI**,
  otherwise the CLI cannot find the IPC socket. It then checks the instance
  lock and exits with code 6 if the web process is running.
- **With the web process running, the CLI only forwards.** Caddy admin, server,
  template directory, and reconcile interval then come from the web process;
  the CLI flags/env for these parameters do not change the running owner.
  `CADDYKIT_DATA_DIR` and `XDG_RUNTIME_DIR` must match, however, so the CLI can
  find the socket.
- **`EDITOR` is split on spaces**, not by a shell. Arguments containing
  spaces/quoting are not supported. Example: `EDITOR="/usr/bin/nano -w"`.
- **Remote admin:** `SSL_CERT_FILE`/`SSL_CERT_DIR` only affect an `https://`
  target. They are irrelevant for `http://localhost` or Unix sockets.
  `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, and `ALL_PROXY` are **not**
  evaluated; the admin client uses no proxy.
- **No env variants** exist by design for `--allow-remote-admin` and
  `--allow-insecure`; both are security-relevant, explicit CLI flags.
- `TZ` only affects timestamps in log output (Go standard), not the business
  logic. The UI shows times in the browser-local format.

Example:

```sh
export CADDYKIT_CADDY_ADMIN=http://127.0.0.1:2019
export CADDYKIT_DATA_DIR="$HOME/.caddykit"
export CADDYKIT_TEMPLATE_DIR="$HOME/.config/caddykit/templates"
export CADDYKIT_DOMAIN=proxy.dev.example.com
export CADDYKIT_RECONCILE_INTERVAL=15s
export EDITOR="vi"

caddykit web
```

#### For builds and tests only

These variables are not read by the `caddykit` binary, but by the `Makefile`,
integration tests, or Playwright:

| Variable | Used by | Meaning | Default |
|---|---|---|---|
| `GO` | `Makefile` | Go command | `go` |
| `NPM` | `Makefile` | npm command | `npm` |
| `VERSION` | `Makefile` | Version via `-X main.version=…` | `git describe --tags --always --dirty`, otherwise `dev` |
| `CADDY_IMAGE` | `test/integration` | Caddy container image | `caddy:2.10`; `make integration` sets `caddy:2.10` and `caddy:2.11` in sequence |
| `CADDYKIT_E2E_BASE_URL` | Playwright | Base URL of a running instance | — (tests are skipped) |
| `CADDYKIT_E2E_PASSWORD` | Playwright | Startup password for the E2E login | — (tests are skipped) |
| `PATH` | Build/tests | `go`, `npm`, `docker`, editor | Process PATH |

`make integration` uses the Docker CLI; its own variables (e.g. `DOCKER_HOST`)
apply but are not evaluated by caddykit. `GOTOOLCHAIN` is a stock Go variable;
notes on the golangci-lint mismatch are in [TESTING.md](TESTING.md).

## CLI

```text
caddykit web --domain <d> [options]

caddykit routes list [--all] [--json]
caddykit routes show <route> [--json]
caddykit routes add --domain <d> --upstream <host:port> [options]
caddykit routes add --template <name> --param k=v ...
caddykit routes edit <route> [--json-edit | form flags]
caddykit routes adopt <route|host> [--yes]
caddykit routes unmanage <route> [--yes]
caddykit routes delete <route> [--yes]

caddykit conflicts list [--json]
caddykit conflicts resolve <route> --action take-current|apply-desired|unmanage [--yes]

caddykit undo [--yes] [--json]
caddykit templates list [--json]
caddykit templates show <name> [--json]
```

Form flags for `add`/`edit`: `--path-prefix`, `--strip-prefix`,
`--upstream-tls`, `--tls-server-name`, `--insecure-skip-verify`,
`--set-request-header K=V`, `--del-request-header K`,
`--set-response-header K=V`, `--del-response-header K`.
`--json-edit` opens the route object in `$EDITOR` (default `vi`) and reopens it
on validation errors.

### Selector

`<route>` is a UUID prefix (≥ 8 characters, unique), an exact `@id`, or a
hostname (only if unique; otherwise a candidate list is shown).

### Exit codes

| Code | Meaning |
|---|---|
| 0 | ok |
| 1 | internal error |
| 2 | usage/validation |
| 3 | conflict, confirmation required, undo blocked |
| 4 | Caddy unreachable |
| 5 | auth/lock/IPC |
| 6 | second instance |

Non-interactive without `--yes` exits with 3. `--json` returns
machine-readable output including the diff; with `--json` and without `--yes`,
only the preview is printed.

If the web process is running, the CLI forwards all actions to the state owner
via the Unix socket. If it is not running, the CLI performs a one-shot
operation under `caddykit.lock` and `state.lock` — without reconcile and
without UI.

## Web UI

Dark-first, dense, keyboard-first. Pages: login, dashboard, create route
(quick + advanced), template wizard, route detail (structured handler
editor/JSON/Caddyfile), conflict center, undo, templates, settings.

- `⌘K`/`Ctrl+K`: command palette
- `g d`, `g c`, `g u`: dashboard, conflicts, undo
- `/`: focus filter
- status polling every 5 s and on `visibilitychange`
- JSON tab loads CodeMirror 6 only on demand
- `caddykit/self` is protected as a system route
- 409 `PREVIEW_STALE` → the preview is refreshed and confirmed again

### Handler editor

The form tab of the route detail page edits the complete route tree:

- Match: host, path, and header matchers; additional matchers via "Advanced".
- Add, remove, and reorder handlers: `reverse_proxy`, `headers`, `rewrite`,
  `encode`, `subroute` (recursive with its own routes), `static_response`,
  `file_server`, and arbitrary handlers.
- Unknown handlers and fields are preserved and editable via the per-object
  advanced fields; the JSON tab remains the complete fallback.
- Foreign routes are adopted on save; `caddykit/self` remains locked.

### Caddyfile preview

The Caddyfile tab and the preview dialog render the route as a readable
Caddyfile approximation. Caddy cannot officially export JSON to Caddyfile;
unknown handlers and fields that cannot be mapped appear as a comment and
warning. The output is **read-only** and is never sent to or applied by Caddy.
It can expose sensitive header values.

Measured: approx. **206 KB gzip JS + 6 KB gzip CSS + 87 KB fonts** (total
≈ 299 KB, budget 400 KB). The earlier FCP measurement was approx. **76 ms**
(Chrome, warm server); it has not been re-measured since the handler editor.
Details in [TESTING.md](TESTING.md).

## How it works

- **Desired state** (`state.json`): only explicitly managed routes
  (`create`, `adopt`, `template`) with a stable `@id`, snapshot, and anchor.
- **Identity**: new routes `caddykit/r/<uuidv7>`; foreign `@id`s are never
  overwritten on adoption.
- **Writes**: fresh read + Etag → diff → confirmation → `If-Match` write →
  verification. 412 is retried up to 3× with a fresh read; if the target
  changes in the meantime, the preview becomes invalid.
- **OpJournal**: `prepared → caddy_written`; after a crash, a comparison of
  the actual Caddy state against the before/after image decides. No
  unambiguous match ⇒ `OP_UNCLEAR` and a write lock until resolved.
- **Reconcile**: only in the web process. Missing managed routes are restored
  automatically in a safe context; foreign routes are never deleted. External
  changes, collisions, duplicate `@id`s, and unclear operations produce
  conflicts instead of overwrites.
- **Undo**: exactly one persistent user step, survives restarts, and checks
  the current actual state before reverting. Automatic restores, self-route
  lifecycle, and password rotation do not overwrite undo. No redo.
- **Conflict resolution**: `take-current`, `apply-desired`, `unmanage` — each
  with a fresh preview and confirmation; inapplicable actions carry a
  machine-readable `block_reason`.

## Security

- Data directory `0700`, all state files `0600`; atomic writes
  (temp + fsync + rename).
- Exclusive `flock` instance lock; a second web process ⇒ exit 6.
- Startup password: 128-bit random, printed once to stdout, never logged;
  constant comparison time; rate limit of 5 failed attempts/minute/IP with
  exponential backoff up to 15 min.
- Sessions server-side, cookie `Secure; HttpOnly; SameSite=Strict`, idle 30 min,
  absolute 12 h, process exit ⇒ invalid.
- CSRF: `caddykit_csrf` (not HttpOnly) + `X-CSRF-Token`, checked server-side.
- IPC: Unix socket `0600`, token file `0600`, header `X-Devproxy-Token` for
  write access.
- Sensitive header values are redacted in previews/diffs (display only, not
  storage protection). The JSON editor warns about visible values.
- No startup passwords, session tokens, or IPC tokens in regular logs.

## Files

In the data directory:

| File | Content |
|---|---|
| `state.json` | Desired state (server + managed routes) |
| `undo.json` | exactly one undo step |
| `opjournal.json` | max. one open operation |
| `caddykit.lock` | process lock |
| `state.lock` | short file transactions |
| `ipc.token` | token for the local CLI interface |
| `caddykit.sock` | Unix socket (prefers `$XDG_RUNTIME_DIR`) |

## Templates

YAML files, one per template. Embedded defaults: `mcphub` (port 3000) and
`omniroute` (port 20128). A local directory (`--template-dir`) takes
precedence. No `exec`, no scripting: pure `{{param}}` substitution after type
validation (`string`, `domain`, `hostport`, `port`, `bool`, `enum(...)`).
`sensitive: true` values are redacted in previews. A generated route is fully
independent afterwards; template changes never affect existing routes.

## Product limits

No Caddyfile file access, no Caddyfile import/apply, no systemd, no
multi-server, no user management, no container/process management, no remote
registry, no redo, no permanent background daemon without `web`, no global
Caddy changes, no HTTP fallback for the UI. Quick-create covers one domain,
one upstream, optionally a path, and the documented header/TLS fields. The
structured handler editor in the route detail edits arbitrary handlers and
nesting; unknown fields are preserved via advanced fields and the JSON tab.
The Caddyfile view is a read-only approximation and is never applied.

## Documentation

- [TESTING.md](TESTING.md) – acceptance matrix of the 20 criteria
- [DECISIONS.md](DECISIONS.md) – deliberate spec deviations and rationale
- `devproxy-final-plan.md`, `devproxy-spec.md`,
  `designvorschlag-caddy-devproxy.md` – binding planning documents (project input)
