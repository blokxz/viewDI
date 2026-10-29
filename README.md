# viewDI

A native Linux terminal UI that manages, visualises and monitors **Docker** and **Incus**
side by side.

> Status: **Stage 4b** — both engines connected with live metrics: Admin and Resources
> dashboards, a sortable, filterable Table view, a Detail view per instance (`enter`) with
> live logs, inspect tree and charts, lifecycle actions (start, stop, restart,
> pause/resume, delete with a confirmation dialog), create forms for Docker containers
> and Incus containers/VMs, and imports of image tarballs and backups, all tracked with
> progress in the Jobs view (`5`). See `docs/04-development-plan.md`.

![Stage 4b demo](docs/demos/stage-4b.gif)

## Build and run

Requires Go ≥ 1.26 on Linux.

```sh
make build          # → bin/viewdi
make mock           # run with fake engines, no daemons needed
./bin/viewdi --help
```

Keys: `1`–`6` switch views, `tab` cycles panes, `T` opens the theme picker (live preview),
`?` shows every binding, `q` quits. In the Table view (`3`): `/` filter, `e` engine
filter, `o`/`O` sort, `P` Incus project, `ctrl+r` reconnect. The full key map is in
`docs/03-mockups.md`. In Resources (`2`): `o`/`O` sort the top consumers, `+`/`-` change
the refresh rate. `enter` opens the Detail view: `tab`/`shift+tab` switch its tabs, `esc`
goes back; in Logs `p` pauses, `/` filters (regex), `S` cycles stdout/stderr, `w` wraps,
`G`/`gg` jump; in Inspect `←`/`→` fold, `/` searches, `y` copies the path; in Overview
`v` reveals a masked value. Actions: `s` start, `x` stop, `R` restart, `F` pause/resume,
`d` delete — on the rows marked with `space`, or the selected instance; `5` lists the jobs
and `c` cancels one. `n` creates a container or VM: a form with local image suggestions
(`ctrl+n`/`ctrl+p`), registry or `images:` search (`ctrl+f`), ports, environment, volumes,
limits and more. `i` imports a Docker image or rootfs tarball, an Incus backup or an
Incus image through a file picker. `--read-only` disables every action. `--mock=chaos` cycles through daemon failures,
`--mock=logflood` floods a log at 1 000 lines/s.

Metrics are read from cgroup v2 files when possible (one pass for every container) and
through the engine APIs otherwise (Incus VMs, rootless setups without readable cgroups);
sampling slows to every 5 s while no metrics view is visible.

Daemons are discovered automatically (`--docker-host`/`DOCKER_HOST`, rootless then
rootful Docker; `--incus-socket`/`INCUS_SOCKET`, then `unix.socket`, then
`unix.socket.user`). Your user needs the `docker` and `incus-admin` (or `incus`) groups;
otherwise viewDI shows the exact fix.

## Configuration

| File | Purpose |
|---|---|
| `~/.config/viewDI/config.toml` | settings (`[ui]`, `[docker] host`, `[incus] socket`, `[alerts]`, `[logs]`, `[actions]`, `[updates]`), never rewritten by the app |
| `~/.config/viewDI/themes/*.toml` | user themes; same name overrides a built-in |
| `~/.config/viewDI/keys.toml` | key overrides, e.g. `[global]` `theme = "ctrl+t"` |
| `~/.local/state/viewDI/state.json` | UI state (last view, theme, table sort, Incus project, last detail tab per engine), written atomically |

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
```

Secret-looking environment values (`*PASSWORD*`, `*TOKEN*`, `*_KEY`…) are masked in the
Overview and Inspect tabs; `v` reveals one at a time in Overview.

Built-in themes: Everforest (dark/light), Dracula, Catppuccin (Mocha/Latte), Monokai,
Gruvbox, Nord, Tokyo Night.

## Development

```sh
make test           # race detector, shuffled, goroutine leak checks
make lint           # golangci-lint v2
make golden-update  # regenerate golden frames after an intended UI change
make fuzz           # every fuzz target for FUZZTIME (default 30s)
make bench
```

Design documents live in `docs/`; `CLAUDE.md` summarises the conventions.
