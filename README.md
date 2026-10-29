# viewDI

A native Linux terminal UI that manages, visualises and monitors **Docker** and **Incus**
side by side.

> Status: **Stage 1** — both engines connected (read-only): a sortable, filterable
> Table view of every container and VM, live engine status, reconnect banner and fix
> hints. Metrics arrive in Stage 2. See `docs/04-development-plan.md`.

![Stage 1 demo](docs/demos/stage-1.gif)

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
`docs/03-mockups.md`. `--mock=chaos` cycles through daemon failures.

Daemons are discovered automatically (`--docker-host`/`DOCKER_HOST`, rootless then
rootful Docker; `--incus-socket`/`INCUS_SOCKET`, then `unix.socket`, then
`unix.socket.user`). Your user needs the `docker` and `incus-admin` (or `incus`) groups;
otherwise viewDI shows the exact fix.

## Configuration

| File | Purpose |
|---|---|
| `~/.config/viewDI/config.toml` | settings (`[ui]`, `[docker] host`, `[incus] socket`, `[updates]`), never rewritten by the app |
| `~/.config/viewDI/themes/*.toml` | user themes; same name overrides a built-in |
| `~/.config/viewDI/keys.toml` | key overrides, e.g. `[global]` `theme = "ctrl+t"` |
| `~/.local/state/viewDI/state.json` | UI state (last view, theme, table sort, Incus project), written atomically |

Invalid files never stop the app: defaults are used and a warning toast explains why.

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
