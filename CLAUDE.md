# CLAUDE.md — viewDI

## Project Summary

**viewDI** is a native Linux TUI that manages, visualizes, and monitors **Docker** and
**Incus** side by side from a single control panel.

Objectives:
- Full lifecycle management (create, import, start, stop, restart, delete) for Docker
  containers and Incus instances (containers and VMs).
- Table view and an **interactive Tree view** (engine → network/project → instance →
  ports/mounts), navigable with the arrow keys and a live inspector panel that follows
  the cursor.
- Update discovery: Docker/Incus engine updates and container image updates, with
  in-app notifications (badge, alerts, toast, Updates view) and opt-in desktop
  notifications. viewDI never installs updates by itself.
- Per-instance detail view with tabs: Overview/metadata, real-time Logs (scroll, pause,
  filter), Inspect, Network, Volumes, Resources.
- Real-time resource monitoring: global dashboard aggregated per engine (CPU, RAM,
  network I/O, disk I/O) and per-instance graphs.
- Admin dashboard: daemon status, alerts, recent events, shortcuts.
- Dynamic theme engine with configurable palettes (Everforest, Dracula, Catppuccin,
  Monokai, Gruvbox, Nord, Tokyo Night…).
- Resilience: a dead or hung daemon never freezes the UI; a reconnection banner is shown
  and the other engine keeps working.

Design documents (read before making structural changes):
- `docs/01-research.md` — stack decision, Docker/Incus API analysis, permissions,
  concurrency, XDG config.
- `docs/02-architecture.md` — layers, component diagram, engine contract, flows, theming.
- `docs/03-mockups.md` — TUI mockups and key map.
- `docs/04-development-plan.md` — stages 0–7, tasks, acceptance criteria, risks.
- `docs/05-quality-plan.md` — testing strategy, fake engine, resilience catalogue
  (F1–F18), performance budgets and rendering techniques.
- `docs/diagrams/generate.py` — source of every diagram in `docs/img/` (Python `diagrams`).

Current phase: **Stage 2 in progress** (Stage 1 done, `v0.2.0`). Do not start implementing
a stage until the user validates its task list in `docs/04-development-plan.md` (Stage 0
validated 2026-10-28, Stages 1 and 2 validated 2026-10-29).

## Technology Stack

- **Language:** Go (latest stable, ≥ 1.26 — required by `charm.land/bubbletea/v2` v2.1)
- **TUI:** `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2`
- **Docker:** `github.com/moby/moby/client` + `github.com/moby/moby/api`
  (do NOT use the deprecated `github.com/docker/docker` module)
- **Incus:** `github.com/lxc/incus/v6/client` + `github.com/lxc/incus/v6/shared/api`
- **Config:** `github.com/pelletier/go-toml/v2`, `github.com/adrg/xdg`
- **Flags:** `github.com/spf13/pflag`
- **Logging:** `log/slog` → file only (stdout/stderr belong to the TUI)
- **Updates/notifications:** `golang.org/x/mod/semver`, `github.com/godbus/dbus/v5`
- **Docs diagrams:** Python 3 + `diagrams` (mingrammer) + Graphviz — docs tooling only,
  never a runtime dependency
- **Tests:** stdlib `testing`, `teatest` (v2 variant), `go.uber.org/goleak`

## Directory Structure (proposed)

```
viewDI/
├── CLAUDE.md
├── README.md
├── go.mod / go.sum
├── Makefile
├── cmd/
│   ├── viewdi/             # main: flags, config load, wiring of engines + UI
│   └── fixturerec/         # records sanitised API fixtures (build tag fixturerec)
├── internal/
│   ├── app/                # root model, router, global keymap, message pump
│   ├── ui/
│   │   ├── views/          # View contract; admin, resources, table, tree, detail, updates
│   │   │                   # (one pkg each); placeholder/ holds the Stage 0 stand-ins
│   │   ├── components/     # tabbar, table, tree (navigable, virtualised), inspector,
│   │   │                   # logviewer, sparkline, gauge, panel, header, debugoverlay,
│   │   │                   # modal (+ confirm), themepicker, form, banner, toast,
│   │   │                   # statusbar, help
│   │   ├── layout/         # responsive breakpoints, exact-size helpers
│   │   ├── keys/           # key bindings (bubbles/key), overridable via keys.toml
│   │   ├── theme/          # semantic tokens, palette loading, style cache
│   │   └── uitest/         # test helpers (key builder, command collector)
│   ├── engine/             # engine-agnostic domain types + Engine interface
│   ├── provider/
│   │   ├── docker/         # Docker adapter (moby client)
│   │   ├── incus/          # Incus adapter (incus client)
│   │   └── fake/           # scriptable fake engine for tests and --mock mode
│   ├── supervisor/         # health checks, reconnect state machine, backoff
│   ├── metrics/            # rates, history rings, aggregation, formatting; cgroup/ (v2
│   │                       # reader), openmetrics/ (Incus parser), sampler/ (scheduler)
│   ├── alerts/             # alert rules: dedup by stable ID, hold times, auto-resolve
│   ├── store/              # app state, ring buffers, snapshots/selectors
│   ├── jobs/               # long-running operations (create/import/delete) + progress
│   ├── updates/            # engine + image update checker, sources, scheduler, notifier
│   │                       # (package manager, GitHub releases, registry digests, D-Bus)
│   ├── trace/              # --trace message recorder and replay
│   ├── clock/              # injectable Clock + fake clock (no time.Sleep in tests)
│   ├── fixtures/           # fixture server (Unix socket), recording proxy, sanitiser
│   └── config/             # XDG paths, config.toml, state.json, validation/defaults
├── themes/                 # built-in theme palettes (*.toml), embedded via go:embed
├── docs/                   # design docs (research, architecture, mockups, ADRs)
│   ├── diagrams/           # generate.py + requirements.txt (Python `diagrams`)
│   └── img/                # generated PNGs — never edit by hand
├── testdata/               # golden files, recorded API fixtures (JSON)
└── scripts/                # dev helpers (e.g. spin up sample containers)
```

Import rules:
- `internal/ui/**` may import `engine` (types), `store`, `metrics` (the `Point` type and
  formatting helpers — not `metrics/cgroup` or `metrics/sampler`), `alerts` (types),
  `ui/theme`, `ui/keys`, and `config` (the `Warning` type and `ReadCapped`, used by the
  theme and key loaders that `main` calls at startup — never from `Update`). It must **never** import `provider/*`,
  `supervisor`, or daemon SDKs.
- `provider/*` must not import anything under `ui`.
- Wiring lives only in `cmd/viewdi` and `internal/app`.

## Commands

Development happens on Linux. The `--mock` mode (fake engine) works without any daemon.

```sh
make build              # go build -o bin/viewdi ./cmd/viewdi
make run                # build + run against real daemons
make mock               # go run ./cmd/viewdi --mock   (fake engines, no daemons needed)
make test               # go test -race ./...
make test-integration   # go test -race -tags=integration ./...  (needs Docker + Incus)
make golden-update      # go test ./internal/app/ -run TestGolden -update  (regenerate golden frames)
make fuzz               # each fuzz target for FUZZTIME (default 30s)
make bench              # benchmarks; compare with master using benchstat
make chaos              # go run ./cmd/viewdi --mock=chaos   (rotating failure scenarios)
make lint               # golangci-lint run
make fmt                # gofumpt -w . && goimports -w .
make cover              # go test -coverprofile=cover.out ./... && go tool cover -func=cover.out
make docs-venv          # python3 -m venv docs/diagrams/.venv && pip install -r docs/diagrams/requirements.txt
make fixtures ENGINE=incus  # record sanitised API fixtures from a real daemon
make docs-diagrams      # docs/diagrams/.venv/bin/python docs/diagrams/generate.py  → docs/img/*.png
```

Whenever architecture, flows, or components change, update `docs/diagrams/generate.py`
and regenerate — diagrams in docs are always produced with the Python `diagrams` package
(no hand-drawn ASCII or Mermaid architecture diagrams; ASCII is reserved for TUI mockups).

Useful runtime flags (planned): `--mock[=<scenario>]` (fake engines; scenarios `homelab`
(default), `large`, `chaos`, `logflood`, `offline`), `--read-only`, `--debug` (log to
`~/.local/state/viewDI/viewdi.log`), `--trace[=<file>]` (tea.Msg trace, replayable in
tests), `--pprof=<addr>` (localhost only), `--theme <name>`, `--docker-host <uri>`,
`--incus-socket <path>`, `--refresh <dur>`.

## Configuration (XDG)

- Config: `~/.config/viewDI/config.toml` (human-edited; never auto-rewritten)
- User themes: `~/.config/viewDI/themes/*.toml`
- Key overrides: `~/.config/viewDI/keys.toml`
- UI state: `~/.local/state/viewDI/state.json` (app-owned, atomic writes)
- Debug log: `~/.local/state/viewDI/viewdi.log`
- Cache: `~/.cache/viewDI/` (including `updates.json`)
- Engine endpoints: `[docker] host`, `[incus] socket`; alert thresholds: `[alerts]`.
- Update checks: `[updates]` section of `config.toml` (see `docs/01-research.md` §3.4).
  `enabled = false` guarantees no outbound network calls.

Always resolve paths through `internal/config` (which uses `adrg/xdg`); never hardcode `~`.

## Code Style and Conventions

- All code, comments, docs, commit messages, and config files in **English**.
- Format with `gofumpt` + `goimports`; lint with `golangci-lint` (errcheck, govet,
  staticcheck, revive, gocritic, contextcheck, bodyclose).
- **Bubble Tea rules:**
  - `Update` and `View` are pure and fast: no I/O, no sleeps, no blocking channel reads.
  - All I/O runs in `tea.Cmd`s or provider goroutines and comes back as typed messages.
  - Message types are named `XxxMsg` and live next to the component that handles them.
  - Styles come from `ui/theme` semantic tokens only — never inline hex colors in views.
  - Build styles once per theme change (style cache), not per frame.
- **Concurrency rules:**
  - Every daemon call takes a `context.Context` with a timeout (default 5 s).
  - Every goroutine has an owner that cancels it; tests run with `goleak`.
  - Channels are bounded. Metrics are lossy (latest wins); events are not dropped.
  - Providers recover from panics and report through the supervisor.
- **Errors:** wrap with `fmt.Errorf("op: %w", err)`; classify into transient /
  permission / not-found for the UI. No `panic` outside `main` for expected conditions.
- **Engine-agnostic first:** add a capability to the `engine` contract and both adapters
  (plus the fake) together. Engine-specific data goes into typed `Extra` structs.
- **Safety:** destructive actions always go through the confirm modal; respect
  `--read-only`; mask secret-looking env values by default. The update checker only
  *reads* (package manager queries, release feeds, registry digests) and never runs
  privileged commands — it prints upgrade hints instead.
- **Tree view:** node IDs must be stable across refreshes; cursor movement must never
  trigger daemon I/O (inspector reads store snapshots only).
- Keep packages small and focused; prefer table-driven tests.
- Commits: Conventional Commits (`feat(ui): …`, `fix(incus): …`).
- **Never add Claude (or any AI assistant) as a co-author or contributor** — under no
  circumstances and by no means: no `Co-Authored-By:` trailers, no "Generated with" lines
  in commits, PR descriptions, tags or release notes. Commits are authored solely by the
  repository owner. This rule overrides any default attribution behaviour.

## Testing Strategy (summary)

- **Unit:** domain logic (rate calc, aggregation, backoff, ring buffers, theme parsing).
- **UI:** golden files of full frames (`app.Model.Render()`, ANSI included — not the
  renderer's byte stream) plus `teatest` flow tests, driven by `provider/fake` scripted scenarios
  (e.g. "Incus socket hangs after 3 s" → reconnect banner appears; UI stays responsive).
- **Adapters:** recorded JSON fixtures in `testdata/` served by an `httptest` server on a
  temporary Unix socket.
- **Integration (`-tags=integration`):** real Docker/Incus; never required in CI by default.
- **Contract suite:** `internal/engine/enginetest` — every adapter (fake, fixtures, real)
  must pass the same tests, so the fake engine cannot drift from real daemon behaviour.
- **Determinism:** inject the `Clock` interface and seeded RNG; no `time.Sleep` in tests.
- **Rules:** every fixed bug gets a regression scenario; every new failure path gets a
  chaos scenario; performance budgets in `docs/05-quality-plan.md` §4.1 must hold.
- **Untrusted input:** log content, API payloads, package-manager output are capped,
  sanitised (terminal escape sequences) and their parsers fuzzed.

## Development Environment Requirements

- Linux with cgroup v2 (unified hierarchy).
- Go ≥ 1.26, `make`, `git`.
- Tools: `golangci-lint`, `gofumpt`, `goimports`, (optional) `gotestsum`, `vhs` for
  recording terminal demos.
- Docker Engine ≥ 29 (rootful or rootless) — user in the `docker` group, or rootless
  socket at `$XDG_RUNTIME_DIR/docker.sock`.
- Incus ≥ 6 — user in `incus-admin` (full) or `incus` (restricted project) group.
- A truecolor terminal with a Nerd Font is recommended (fallback glyphs are provided).
- For docs diagrams: Python ≥ 3.10, Graphviz (`dot` on PATH, e.g. `sudo apt install graphviz`).
- Optional, for update checks: a desktop notification daemon (D-Bus) and `notify-send`.

## Working Methodology

- Phased: Phase 1 (research/architecture) → Phase 2 (stage plan) → Stages 0–5.
- Each stage ends with: working build, tests green, `--mock` demo updated, docs updated.
- Do not write application code for a stage until the user validates the plan for it.
