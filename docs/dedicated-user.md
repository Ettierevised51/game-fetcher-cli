# Running gamefetcher under a dedicated user

The tool deliberately does not manage system users or call sudo — it runs as
whoever invoked it. Isolating game servers under a dedicated user is a
one-time OS-level setup described below (Linux). After that everything works
as usual.

The examples use a user named `gamefetcher`. Any name works; you can also
create one user per game (`gf-rust`, `gf-valheim`, …) for stricter isolation —
each gets its own home, config, state and steamcmd copy, so they never
interfere.

## 1. Create the user

System user, no login shell, own home (steamcmd, Steam Guard sentry files,
state and the app-index cache will live there):

```sh
sudo useradd --system --create-home --home-dir /srv/gamefetcher \
  --shell /usr/sbin/nologin gamefetcher
```

Add yourself to its group so you can read/edit game files without sudo
(**takes effect after re-login**; until then use `sudo -u gamefetcher`):

```sh
sudo usermod -aG gamefetcher "$USER"
```

## 2. Game directories

Owned by the user; setgid bit so new files inherit the group:

```sh
sudo mkdir -p /srv/gamefetcher/games
sudo chown gamefetcher:gamefetcher /srv/gamefetcher/games
sudo chmod 2775 /srv/gamefetcher/games
```

Point profile `install_dir`s inside it — the tool creates the per-game
subdirectories (`install_dir` and `steamapps/`) itself on first run.

## 3. Binary and config

Binary system-wide; config either in the system layer or in the user's home
(the tool reads both — see the config layering):

```sh
sudo cp gamefetcher /usr/local/bin/
sudo mkdir -p /etc/gamefetcher
# let the gamefetcher group edit the config without sudo (root keeps ownership)
sudo chgrp -R gamefetcher /etc/gamefetcher
sudo chmod 2775 /etc/gamefetcher
sudo tee /etc/gamefetcher/config.yaml >/dev/null <<'EOF'
auto_install_steamcmd: true          # installs into ~gamefetcher/.local/share
# download_rate_limit: 20M
EOF
sudo chmod 664 /etc/gamefetcher/config.yaml
```

(After re-login your group membership applies and the file is editable
without sudo. Alternatively skip `/etc` entirely and keep the config in the
user layer: `/srv/gamefetcher/.config/gamefetcher/config.yaml` — the tool
reads both.)

`state_path` can stay unset: under the `gamefetcher` user the state lands in
`/srv/gamefetcher/.local/share/gamefetcher/state.json`.

## 4. Running

### One-off commands: `sudo -u`

Always with the user's HOME (`-H` matters — otherwise steamcmd, sentry files
and state end up in *your* home):

```sh
sudo -u gamefetcher -H gamefetcher health-check
sudo -u gamefetcher -H gamefetcher sync
sudo -u gamefetcher -H gamefetcher run --app 1007 --dir /srv/gamefetcher/games/sdk
```

### Interactive session: `sudo su`

For longer sessions, open a shell as the user instead of prefixing every
command. The account has no login shell, so pass one explicitly:

```sh
sudo su -s /bin/bash - gamefetcher
# now a normal session in /srv/gamefetcher with correct HOME:
gamefetcher health-check
gamefetcher sync
exit
```

(`sudo -u gamefetcher -H -s /bin/bash` does the same.)

The first run downloads steamcmd (allowed by `auto_install_steamcmd: true`;
or pass `--allow-install-steamcmd`).

Steam credentials for non-anonymous games — via env passed through sudo, or
exported inside the `sudo su` session:

```sh
sudo -u gamefetcher -H \
  GAMEFETCHER_STEAM_USERNAME=... GAMEFETCHER_STEAM_PASSWORD=... \
  gamefetcher sync
```

(or keep them in a 600-permission env file owned by the user and source it in
the interactive session).

## 5. Scheduled runs

`gamefetcher systemd-gen --user gamefetcher --out units/` generates these
units (server units plus the sync service/timer pair) for you. Written by
hand, it is a systemd timer with `User=` (or the user's crontab):

```ini
# /etc/systemd/system/gamefetcher-sync.service
[Unit]
Description=gamefetcher sync
[Service]
Type=oneshot
User=gamefetcher
ExecStart=/usr/local/bin/gamefetcher sync
```

```ini
# /etc/systemd/system/gamefetcher-sync.timer
[Timer]
OnCalendar=hourly
Persistent=true
[Install]
WantedBy=timers.target
```

`sudo systemctl enable --now gamefetcher-sync.timer`

## Notes

- Sanity check for the whole setup: `sudo -u gamefetcher -H gamefetcher
  health-check` — config, network, steamcmd, Steam login, state dir.
- Game files are group-readable for you after re-login; before that, go
  through `sudo -u gamefetcher`.
- Multiple independent environments = multiple users; nothing is shared
  between them.
