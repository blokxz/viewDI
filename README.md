# viewDI

A native Linux terminal UI that manages, visualises and monitors **Docker** and **Incus**
side by side.

> Status: **Stage 0** — the application shell (layout, keyboard navigation, themes,
> config). Engines arrive in Stage 1. See `docs/04-development-plan.md`.

![Stage 0 demo](docs/demos/stage-0.gif)

## Build and run

Requires Go ≥ 1.26 on Linux.

```sh
make build          # → bin/viewdi
make mock           # run with fake engines, no daemons needed
./bin/viewdi --help
```

Keys: `1`–`6` switch views, `tab` cycles panes, `T` opens the theme picker (live preview),
`?` shows every binding, `q` quits. The full key map is in `docs/03-mockups.md`.

## Configuration

| File | Purpose |
|---|---|
| `~/.config/viewDI/config.toml` | settings (`[ui]`, `[updates]`), never rewritten by the app |
| `~/.config/viewDI/themes/*.toml` | user themes; same name overrides a built-in |
| `~/.config/viewDI/keys.toml` | key overrides, e.g. `[global]` `theme = "ctrl+t"` |
| `~/.local/state/viewDI/state.json` | UI state (last view, theme), written atomically |

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
