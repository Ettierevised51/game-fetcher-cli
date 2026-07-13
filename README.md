# gamefetcher

CLI for installing and updating Steam game dedicated servers and Steam
Workshop mods, wrapping `steamcmd`. Go, no API keys, no scraping, no external
runtime dependencies — one static binary.

What it does for you:

- **One-off installs**: `run --app <id> --dir <path>` (or resolve the app by
  name with an interactive picker) installs a server plus its Workshop mods,
  and can save the whole thing as a named profile.
- **Keeping servers updated**: profiles and groups in YAML config, a state
  cache, and `sync` that downloads only new/changed items — cron/systemd
  friendly (`--json`, `--log-level quiet`, generated units and timer).
- **The steamcmd babysitting**: install-dir preparation and verification,
  retry classification, Steam Guard handling, parallel workers, an optional
  built-in download rate limit — all handled for you.

gamefetcher never manages the game server process itself (start/stop/restart
stays with systemd or you) and never manages system users.

## Installation

Download the latest release for your platform (Linux, amd64/arm64 are
detected automatically) and put it into `PATH`:

```sh
curl -fLo gamefetcher "https://github.com/Austrum-lab/game-fetcher-cli/releases/latest/download/gamefetcher-linux-$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')"
sudo install -m755 gamefetcher /usr/local/bin/
gamefetcher --version
```

(any directory in `PATH` works; `/usr/local/bin` is the conventional place
for manually installed binaries). The result is a single fully static binary
with no runtime dependencies.

`steamcmd` itself is either taken from the PATH, or downloaded from the
Valve CDN on first use when allowed (`--allow-install-steamcmd` /
`auto_install_steamcmd: true`).

### Building from source

```sh
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X github.com/Austrum-lab/game-fetcher-cli/internal/cli.version=$(git describe --tags 2>/dev/null || echo dev)" \
  -o gamefetcher ./cmd/gamefetcher
sudo install -m755 gamefetcher /usr/local/bin/
```

`CGO_ENABLED=0` keeps the net package off libc, so the binary runs on any
distro. Development build: `go build ./...`; tests: `go test ./...`;
run from source: `go run ./cmd/gamefetcher --help`.

### Shell completion

Bash (`zsh`/`fish`/`powershell` work the same way):

```sh
gamefetcher completion bash | sudo tee /usr/share/bash-completion/completions/gamefetcher
```

## Quick start

### A dedicated server, saved as a profile

Find the app id by name (offline fuzzy index, no API keys), install, and
persist the flags as a profile in one go:

```sh
gamefetcher search palworld
# 2394010	Palworld Dedicated Server
gamefetcher run --app 2394010 --dir /srv/palworld \
  --save-as-profile palworld --allow-install-steamcmd
```

From now on `gamefetcher run palworld` repeats the install and
`gamefetcher sync` updates it when Valve ships a new build. `--game palworld`
instead of `--app` does the search + an interactive picker inline.

### A modded server from a Workshop collection

Mods are selected by id or by Workshop collection — collections re-expand on
every run/sync, so items added to the collection later flow in automatically.
Project Zomboid as an example:

```sh
gamefetcher search zomboid
# 380870	Project Zomboid Dedicated Server
gamefetcher search --collection 1234567890          # list the collection first
gamefetcher search --collection 1234567890 --pick   # ...or multi-select a subset
gamefetcher run --app 380870 --dir /srv/zomboid \
  --collection 1234567890 --mod 2313387159 -s zomboid
```

The base/client game id that Workshop mods belong to (not the server's app
id) is resolved automatically from the mod metadata; `-B/--base-app`
overrides it.

### Hourly auto-updates via systemd

`systemd-gen` writes one `<profile>.service` per profile (ExecStart resolved
from Steam's own launch metadata when possible) plus an **optional**
`gamefetcher-sync.service`/`.timer` pair that runs `sync` on an interval:

```sh
gamefetcher systemd-gen --out units/ --every 1h
sudo cp units/*.service units/*.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now gamefetcher-sync.timer   # skip for manual-only updates
```

Existing files are never overwritten silently — a diff is shown and
confirmation asked.

### Running under a dedicated user

For isolating game servers under a no-login system user (recommended for
anything internet-facing), see
[`docs/dedicated-user.md`](docs/dedicated-user.md) — the tool itself never
calls sudo or manages users; that document covers the one-time OS setup.

## Commands

```sh
gamefetcher run --app 258550 --dir /srv/rust --mod 111   # one-off install/update
gamefetcher run --game "rust dedicated" --dir /srv/rust  # ... resolving the app by name (picker)
gamefetcher run rust                                      # ... or by profile
gamefetcher sync                                          # update only new/changed items (state cache)
gamefetcher search valheim dedicated                      # name → appid (offline fuzzy index)
gamefetcher search --collection 1234567890                # list a workshop collection's items
gamefetcher health-check                                  # config / network / steamcmd / login / state / runner
gamefetcher logout                                        # forget the cached Steam session (re-asks Steam Guard)
gamefetcher status                                        # per-profile install & sync state
gamefetcher systemd-gen --out units/                      # generate server units + optional sync timer
gamefetcher completion bash|zsh|fish|powershell           # shell completion
```

Every command has `--help`/`-h`; short flag variants exist everywhere.

### Command reference

| Command | Args | Flags (short/long) |
|---|---|---|
| `run` | `[profile\|group ...]` | `-a/--app`, `-g/--game` (name → picker), `-d/--dir`, `-m/--mod` (repeatable), `-C/--collection` (repeatable), `-B/--base-app`, `-p/--platform windows\|linux\|macos`, `-b/--branch`, `--branch-password`, `-v/--validate` (apps and mods), `-n/--dry-run`, `-j/--json`, `-s/--save-as-profile` |
| `sync` | `[profile\|group ...]` | `-f/--force-all` (alias `--recheck`), `-n/--dry-run`, `-j/--json` |
| `search` | `<query>...` | `-C/--collection <id>` (list a workshop collection instead), `-p/--pick` (interactive multi-select for a subset), `-l/--limit` (default 20), `-j/--json` |
| `status` | `[profile\|group ...]` | `-j/--json` |
| `health-check` | — | `-j/--json` |
| `logout` | — | — |
| `systemd-gen` | `[profile\|group ...]` | `-o/--out` (default `.`), `-u/--user`, `-e/--every` (default `1h`), plus diff+confirm on existing files |
| `completion` | `bash\|zsh\|fish\|powershell` | — |

Global flags (every command): `-c/--config <file>`, `--allow-install-steamcmd`,
`-L/--log-level quiet|normal|verbose`, `-U/--steam-user`, `-P/--steam-password`.
On the bare `gamefetcher`, `-v/--version` prints the version (inside `run`,
`-v` means `--validate`).

Notes: `run` installs unconditionally, `sync` diffs against the state cache
first; `run -s <name>` persists explicit flags (including `--collection`) as a
profile **before** the downloads start (a failed run does not lose it; the
resolved `base_app_id` is added after a successful one) — it shows a diff and
asks before overwriting an existing one, and writes to the `--config` file,
an existing `./gamefetcher.yaml`, or `~/.config/gamefetcher/config.yaml`
otherwise.

## Config

Layers, **later wins** (each layer overrides the previous per key; nested
mappings deep-merge, scalars and lists are replaced):

1. built-in defaults
2. `/etc/gamefetcher/config.yaml` (system)
3. `~/.config/gamefetcher/config.yaml` (user, XDG)
4. `./gamefetcher.yaml` in the current directory, or the `--config <file>`
   (which then must exist)
5. environment variables (`GAMEFETCHER_*`)
6. CLI flags

Next to every file layer, `conf.d/*.yaml` drop-ins and files named by its
`include:` are merged in too. Profile inheritance via `extends` is resolved
after all layers are merged, so a profile may extend one from another file.

```yaml
auto_install_steamcmd: true    # fetch steamcmd from Valve CDN when missing
download_rate_limit: 20M       # total cap via the built-in local proxy
parallelism: 4                 # forced to 1 for non-anonymous logins
profiles:
  rust:
    app_id: 258550             # dedicated server app
    install_dir: /srv/rust
    mods: [2714791661]         # explicit workshop ids
    collections: [1234567890]  # collections re-expand on every sync
groups:
  all: [rust]
```

### Config reference

Top-level keys (each overridable by the layer above it):

| Key | Default | Meaning |
|---|---|---|
| `auto_install_steamcmd` | `false` | fetch steamcmd from the Valve CDN when missing (flag: `--allow-install-steamcmd`) |
| `parallelism` | `4` | concurrent steamcmd processes; forced to 1 for non-anonymous logins |
| `download_rate_limit` | off | total cap via the built-in proxy, e.g. `20M` (K/M/G, binary) |
| `steamcmd_path` | auto | explicit steamcmd binary |
| `steam_user` | anonymous | Steam account for non-anonymous downloads (username only — passwords never live in YAML) |
| `state_path` | `~/.local/share/gamefetcher/state.json` | sync-state JSON |
| `app_list_max_age` | `7d` | how long the cached app index stays fresh (durations take `s`/`m`/`h`/`d`) |
| `app_list_urls` | steam-appdb | app-index database URLs |
| `retry.{login,web_api,download}` | see below | `max_attempts`, `base_delay`, `max_delay`, `rate_limit_delay` |
| `include` | — | extra config files; `conf.d/*.yaml` next to a layer file is picked up automatically |
| `groups.<name>` | — | list of profile names run together |

Profile keys: `app_id` (required), `install_dir` (required), `mods`,
`collections`, `base_app_id` (auto-resolved from workshop metadata when
omitted), `platform` (auto-detected; Windows build offered when nothing
native exists), `branch` + `branch_password` (beta branches; password only
for protected ones), `extends` (profile inheritance).

Environment (overrides files, loses to flags):
`GAMEFETCHER_PARALLELISM`, `GAMEFETCHER_AUTO_INSTALL_STEAMCMD`,
`GAMEFETCHER_DOWNLOAD_RATE_LIMIT`, `GAMEFETCHER_STEAMCMD_PATH`,
`GAMEFETCHER_STATE_PATH`; Steam credentials (only for non-anonymous games) —
`GAMEFETCHER_STEAM_USERNAME`/`GAMEFETCHER_STEAM_PASSWORD` or an interactive
hidden prompt, never from YAML.

## Steam login

Anonymous by default (enough for most dedicated servers). For games that
need an account: `-U/--steam-user` flag, `GAMEFETCHER_STEAM_USERNAME`, or
`steam_user` in the config — in that order (password via
`-P/--steam-password`, env, or an interactive hidden prompt; the prompt
names where the username came from). `-U anonymous` forces the anonymous
login past env and config for a one-off. `gamefetcher logout` deletes
steamcmd's cached session (login tokens + sentry files; steamcmd's own
`logout` console command leaves them behind), so the next login asks for
the password and a fresh Steam Guard code. On the first login from a
new machine Steam Guard wants a one-time code — the tool asks for it itself
and passes it to steamcmd via `set_steam_guard_code`; the sentry is cached
after that and every later run is non-interactive. Headless setups: run
`gamefetcher health-check` once interactively to get the code prompt.
Non-anonymous downloads run with parallelism 1 (a second login with the
same account would replace the session).

## Output

By default (`--log-level normal`) steamcmd output is filtered down to the
lines that matter — download starts, successes, errors — prefixed with the
item (`[app 2394010]`, `[mod 12345]`, `[mods]`), and app download/verify
progress renders as an in-place progress bar. The other levels:

- `-L verbose` — raw steamcmd output plus extra detail (for debugging);
- `-L quiet` — errors and interactive prompts only (for cron/systemd).

Workshop items are downloaded in batches: mods are split across the workers
(`parallelism`) and each steamcmd process handles a whole batch of
`workshop_download_item` commands, instead of one process per mod. Failed
items are retried without re-running the succeeded ones.

## Retries

**Downloads are not retried by default** (`retry.download.max_attempts: 1`):
a failed item is simply picked up again by the next `sync`. Login and Web
API calls do retry transient failures (timeouts, HTTP 429/5xx) with
exponential backoff + jitter; hopeless errors ("No subscription", "Invalid
password", Steam Guard denial, a non-writable install dir) always fail
immediately. "Rate Limit Exceeded" on login waits `rate_limit_delay` instead.

Defaults per operation:

| Operation | `max_attempts` | `base_delay` | `max_delay` | `rate_limit_delay` |
|---|---|---|---|---|
| `retry.download` | 1 (off) | 5s | 2m | 15m |
| `retry.login` | 3 | 10s | 5m | 15m |
| `retry.web_api` | 5 | 1s | 30s | 5m |

Turning download retries on:

```yaml
retry:
  download:
    max_attempts: 3
```

## Docs

- [`docs/dedicated-user.md`](docs/dedicated-user.md) — running under a dedicated no-login user
- [Austrum-lab/steam-appdb](https://github.com/Austrum-lab/steam-appdb) — the app-id
  database `search` uses (games + all dedicated servers, anonymous PICS, refreshed every 2h)

## Development

```sh
go build ./...   # compile check of all packages (no binary)
go test ./...    # tests
go vet ./...     # vet
go run ./cmd/gamefetcher --help   # run from source
```

## License

MIT — see [LICENSE](LICENSE).
