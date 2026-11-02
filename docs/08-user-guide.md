# User guide

Everything the README does not cover: the keys of each view, how metrics and update checks work, and how the daemons are found. The complete key map, with mockups, is in [`03-mockups.md`](03-mockups.md); press `?` inside viewDI for the live bindings.

## Keys

`1`–`6` switch views, `tab` cycles panes, `T` opens the theme picker (live preview),
`?` shows every binding, `q` quits. In the Table view (`3`): `/` filter, `e` engine
filter, `o`/`O` sort, `P` Incus project, `ctrl+r` reconnect. The full key map is in
`docs/03-mockups.md`. In Resources (`2`): `o`/`O` sort the top consumers, `+`/`-` change
the refresh rate. `enter` opens the Detail view: `tab`/`shift+tab` switch its tabs, `esc`
goes back; in Logs `p` pauses, `/` filters (regex), `S` cycles stdout/stderr, `w` wraps,
`G`/`gg` jump; in Inspect `←`/`→` fold, `/` searches, `y` copies the path; in Overview
`v` reveals a masked value. In the Tree view (`4`): `↑↓`/`jk` move, `→`/`l` open or go to
the first child, `←`/`h` close or go to the parent, `[`/`]` siblings, `space` folds,
`*`/`-` open all below / close all, `g` cycles the grouping (network, project, compose,
engine only, lab), `J` jumps to the other networks of a machine, `/` filters (name, image,
network, IP) with `ctrl+n`/`ctrl+p` between matches. `L` opens the lab menu: create a
lab, add machines of either engine, link them (a dot runs along a link while both ends
run, faster with more traffic), unlink, rename or delete a lab. Labs never touch the
daemons, so they work under `--read-only`. Actions: `s` start, `x` stop, `R` restart, `F` pause/resume,
`d` delete — on the rows marked with `space`, or the selected instance; `5` lists the jobs
and `c` cancels one. `n` creates a container or VM: a form with local image suggestions
(`ctrl+n`/`ctrl+p`), registry or `images:` search (`ctrl+f`), ports, environment, volumes,
limits and more. `i` imports a Docker image or rootfs tarball, an Incus backup or an
Incus image through a file picker. In Updates (`6`): `enter` opens the release notes,
`z` snoozes an update for 7 days, `I` ignores this version (press again to undo), `U`
checks now from any view; `p` pulls a Docker image, `f` refreshes an Incus image, `c`
recreates the Docker containers of an image on the new build after showing what is kept
(name, env, ports, mounts, networks, restart policy, labels) — if anything fails the
original container gets its name back and runs again. Compose containers get the
`docker compose up -d` hint instead. `--read-only` disables every action. `--mock=chaos` cycles through daemon failures,
`--mock=logflood` floods a log at 1 000 lines/s.

Metrics are read from cgroup v2 files when possible (one pass for every container) and
through the engine APIs otherwise (Incus VMs, rootless setups without readable cgroups);
sampling slows to every 5 s while no metrics view is visible.

Update checks run 30 s after startup, then every `engine_interval` / `image_interval`.
Engine versions come from the package manager (read-only `apt-cache`, `pacman`, `dnf`,
`zypper` queries) or GitHub releases (`GITHUB_TOKEN` is honoured, never stored); images
are compared by registry digest (Docker, through the daemon, so `docker login` applies)
or simplestreams fingerprint (Incus). viewDI installs nothing on its own: it shows the exact
upgrade command. `[updates] enabled = false` turns every check, and every network call,
off. `--mock` uses fake sources; `--mock=offline` fails every check.
`notify_desktop = true` adds a desktop notification for each new batch of updates at or
above `notify_min_level` (D-Bus, then `notify-send`); `--mock --mock-notify` tries it
with the fake sources.

Daemons are discovered automatically (`--docker-host`/`DOCKER_HOST`, rootless then
rootful Docker; `--incus-socket`/`INCUS_SOCKET`, then `unix.socket`, then
`unix.socket.user`). Your user needs the `docker` and `incus-admin` (or `incus`) groups;
otherwise viewDI shows the exact fix.

## Configuration reference

| File | Purpose |
|---|---|
| `~/.config/viewDI/config.toml` | settings (`[ui]`, `[tree]`, `[docker] host`, `[incus] socket`, `[alerts]`, `[logs]`, `[actions]`, `[updates]`), never rewritten by the app |
| `~/.config/viewDI/themes/*.toml` | user themes; same name overrides a built-in |
| `~/.config/viewDI/keys.toml` | key overrides, e.g. `[global]` `theme = "ctrl+t"` |
| `~/.local/state/viewDI/state.json` | UI state (last view, theme, table sort, Incus project, last detail tab per engine, tree grouping and folds), written atomically |
| `~/.local/share/viewDI/labs.json` | labs: their machines (engine, project, name) and links, written atomically; `--mock` never touches it |

Invalid files never stop the app: defaults are used and a warning toast explains why.

Alert thresholds (defaults shown):

```toml
[alerts]
memory_percent = 90      # of the memory limit…
memory_for     = "30s"   # …sustained this long
cpu_percent    = 90      # of the CPU limit (or of one core without a limit)
cpu_for        = "1m"
restart_count  = 3       # starts…
restart_window = "10m"   # …within this window
unhealthy_for  = "0s"    # failing healthcheck

[logs]
buffer_lines = 10000     # log lines kept per open Logs tab (100–200000)

[actions]
stop_timeout = "10s"     # grace period of stop/restart before killing (1s–10m)

[ui]
ascii_icons = false      # ASCII icons and guides in the Tree view (automatic on
                         # TERM=linux and non-UTF-8 locales)

[tree]
animation    = true      # dots running along lab links (off with NO_COLOR)
animation_ms = 125       # frame interval (40–1000)
```

Secret-looking environment values (`*PASSWORD*`, `*TOKEN*`, `*_KEY`…) are masked in the
Overview and Inspect tabs; `v` reveals one at a time in Overview.

Built-in themes: Everforest (dark/light), Dracula, Catppuccin (Mocha/Latte), Monokai,
Gruvbox, Nord, Tokyo Night.
