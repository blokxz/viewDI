# viewDI

A native Linux terminal UI that manages, visualises and monitors **Docker** and **Incus**
side by side.

> Status: **Stage 2** — both engines connected (read-only) with live metrics: Admin
> dashboard (daemon cards, alerts, recent events), Resources dashboard (per-engine CPU,
> RAM, network and disk with sparklines, top consumers), and a sortable, filterable Table
> view with live CPU and memory. Detail views arrive in Stage 3. See
> `docs/04-development-plan.md`.

![Stage 2 demo](docs/demos/stage-2.gif)

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
the refresh rate. `--mock=chaos` cycles through daemon failures.

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
| `~/.config/viewDI/config.toml` | settings (`[ui]`, `[docker] host`, `[incus] socket`, `[alerts]`, `[updates]`), never rewritten by the app |
| `~/.config/viewDI/themes/*.toml` | user themes; same name overrides a built-in |
| `~/.config/viewDI/keys.toml` | key overrides, e.g. `[global]` `theme = "ctrl+t"` |
| `~/.local/state/viewDI/state.json` | UI state (last view, theme, table sort, Incus project), written atomically |

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
```

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
