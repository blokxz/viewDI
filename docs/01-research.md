# viewDI — Phase 1 Research

Status: **Validated (Phase 1)** · Date: 2026-10-28

This document covers the technology stack decision, the daemon API analysis, and the
additional research points (permissions, concurrency, configuration persistence).

---

## 1. Technology Stack Selection

### 1.1 Candidates

| Criterion | Python (Textual / Rich) | Go (Bubble Tea / Lip Gloss) | Rust (Ratatui) |
|---|---|---|---|
| Runtime performance | Interpreted; noticeable CPU at high refresh rates with many widgets | Compiled, GC'd; low CPU, fast startup | Compiled, no GC; best raw performance |
| Async model for real-time metrics | `asyncio` + Textual workers; good, but GIL limits CPU-bound parsing | Goroutines + channels; trivial to run N streams concurrently | `tokio`; excellent, but more ceremony (lifetimes, `Send` bounds) |
| Complex tabs / tables / layouts | Best out of the box (DataTable, TabbedContent, CSS-like styling) | Good: Bubbles v2 (table, viewport, textinput, help), Lip Gloss v2 (layout, tree); tabs are a small custom component | Good primitives (Table, Tabs, Chart, Sparkline) but immediate-mode; app state/focus handling is all on you |
| **Docker client** | `docker` SDK (official, sync; async needs `aiodocker`) | **Official SDK** (`github.com/moby/moby/client`) — Docker itself is written in Go | `bollard` (community, async, solid) |
| **Incus client** | None official (`pylxd` targets LXD; would need hand-rolled REST) | **Official client** (`github.com/lxc/incus/v6/client`) — Incus itself is written in Go | None; hand-rolled REST + WebSocket over Unix socket |
| Distribution | Needs Python + venv/pipx; slow-ish startup | Single static binary | Single static binary |
| Development speed | Fastest | Fast | Slowest |

### 1.2 Decision: **Go + Bubble Tea v2 + Lip Gloss v2 + Bubbles v2**

Justification:

1. **First-class clients for both daemons.** Docker and Incus are both written in Go and
   both ship their official client libraries in Go. This removes an entire layer of
   risk: event listeners, operation waiting, WebSocket handling for Incus, log stream
   demultiplexing for Docker — all already implemented and maintained upstream.
   With Rust or Python the Incus client would have to be written from scratch.
2. **Concurrency fits the problem.** The app is fundamentally "many independent
   streams (stats, logs, events) feeding one UI". Goroutines + channels model this
   directly, and Bubble Tea's Elm architecture (Model → Update → View) gives a
   single-threaded, deterministic UI loop that never blocks on I/O.
3. **Performance is more than sufficient.** A TUI refreshing at 1–4 Hz is not
   CPU-bound; Go's overhead vs Rust is irrelevant here, while Python would cost
   measurable CPU when parsing dozens of stats streams.
4. **Ecosystem maturity.** Bubble Tea v2 is stable (`charm.land/bubbletea/v2`, v2.0.x
   series with the new "Cursed Renderer"), with Bubbles v2 and Lip Gloss v2 released
   alongside it. Lip Gloss v2 includes a `tree` renderer usable for the Tree view.
5. **Testability.** `teatest` allows golden-file testing of the full TUI, and Go
   interfaces make daemon mocking trivial.

Trade-offs accepted:
- Tabs, gauges, and the log viewer are custom components (small, well understood).
- Charting (decided in Stage 0): `ntcharts/v2` (v2.8.0) is compatible — it builds on
  `charm.land/bubbletea/v2`, `bubbles/v2` and `lipgloss/v2` — but pulls image-processing
  dependencies (`pixterm`, `disintegration/imaging`, `x/image`) and `bubblezone`. The
  sparkline and gauge (Stage 2) are therefore **in-house** braille/block components
  (≈150 LOC). `ntcharts/v2` stays an option for the larger time-series charts of the
  Resources tab (Stage 3) if the in-house chart falls short.

### 1.3 Core dependency set (proposed)

| Purpose | Module |
|---|---|
| TUI runtime | `charm.land/bubbletea/v2` |
| Components | `charm.land/bubbles/v2` |
| Styling / layout / tree | `charm.land/lipgloss/v2` |
| Docker client | `github.com/moby/moby/client` + `github.com/moby/moby/api` (Docker ≥ v29 split; the old `github.com/docker/docker` module is deprecated) |
| Incus client | `github.com/lxc/incus/v6/client` + `github.com/lxc/incus/v6/shared/api` |
| XDG paths | `github.com/adrg/xdg` |
| Config format | `github.com/pelletier/go-toml/v2` |
| CLI flags | `github.com/spf13/pflag` (keep it light; no Cobra needed) |
| Logging (file only, never stdout) | `log/slog` (stdlib) |
| Desktop notifications (opt-in) | `github.com/godbus/dbus/v5` (`org.freedesktop.Notifications`), `notify-send` fallback |
| Semantic version comparison | `golang.org/x/mod/semver` |
| Documentation diagrams (docs tooling only) | Python `diagrams` (mingrammer) + Graphviz |
| Charts | in-house sparkline/gauge (`ntcharts/v2` optional for Stage 3, see §1.2) |
| TUI tests | `teatest` (charmbracelet/x, v2 variant) |

Exact versions are pinned in `go.mod` at Stage 0 bootstrap.

---

## 2. APIs and Systems Interface Analysis

### 2.1 Docker

**Transport:** HTTP/1.1 over Unix socket `/var/run/docker.sock` (rootful) or
`$XDG_RUNTIME_DIR/docker.sock` (rootless). The SDK honours `DOCKER_HOST`,
`DOCKER_API_VERSION`, and TLS variables via `client.FromEnv`; API version is
negotiated with `WithAPIVersionNegotiation()`.

**Decision:** use the official SDK, not raw HTTP. It handles version negotiation,
stream demultiplexing (`stdcopy`), and typed responses.

| Feature | Endpoint (SDK method) | Notes |
|---|---|---|
| Health | `GET /_ping` (`Ping`) | Cheap; used by the watchdog |
| Daemon info | `GET /info`, `GET /version` | Engine version, storage driver, cgroup version, counts |
| List | `GET /containers/json?all=1` (`ContainerList`) | Labels, image, state, ports, networks |
| Inspect | `GET /containers/{id}/json` (`ContainerInspect`) | Mounts, network settings, config, health |
| Lifecycle | `POST /containers/{id}/start|stop|restart|pause|unpause`, `DELETE /containers/{id}` | Stop with timeout; delete with `force`/`v` options |
| Create | `POST /containers/create` + start | From a structured form (image, name, ports, env, volumes, network) |
| Import | `POST /images/load` (`docker load` tarball), `POST /images/create?fromSrc=` (`docker import`) | Then create from the imported image |
| Logs | `GET /containers/{id}/logs?follow=1&timestamps=1&tail=N` | Multiplexed stdout/stderr unless the container has a TTY → demux with `stdcopy` |
| Stats | `GET /containers/{id}/stats?stream=1` | ~1 sample/s per container. CPU % computed from `cpu_stats` vs `precpu_stats`; memory = `usage − inactive_file` (cgroup v2) |
| Events | `GET /events` (`Events`) | Container, image, network, volume events; filterable |
| Networks / volumes | `GET /networks`, `GET /volumes` | Feeds the Tree view (network → containers) |

**Cost warning:** one `stats` stream per running container is an open HTTP connection
plus JSON decoding every second. With 50+ containers this is wasteful. See §3.2 for
the hybrid strategy (cgroup reading for the dashboard, SDK streams only for
containers visible in a detail tab).

### 2.2 Incus

**Transport:** REST over Unix socket `/var/lib/incus/unix.socket` (full access), or
`/var/lib/incus/unix.socket.user` (served by `incus-user`, restricted per-user
project). Overridable through `INCUS_SOCKET` / `INCUS_DIR`. Remote servers use
HTTPS + TLS client certificates on port 8443 (future scope).

**Decision:** use the official Go client (`incus.ConnectIncusUnix`), not a CLI wrapper.
Wrapping the `incus` CLI would mean forking processes per refresh and parsing text —
slow, fragile, and impossible to stream properly.

| Feature | Endpoint (client method) | Notes |
|---|---|---|
| Health / info | `GET /1.0` (`GetServer`) | Server version, API extensions, auth status, kernel/driver info |
| List | `GET /1.0/instances?recursion=2` (`GetInstancesFull`) | Includes state in one call; covers containers **and** VMs (`type` field) |
| Projects | `GET /1.0/projects` | Instances are project-scoped; viewDI must expose a project selector |
| State / metrics | `GET /1.0/instances/{name}/state` (`GetInstanceState`) | CPU usage (ns, cumulative → rate), memory, swap, per-NIC counters, disk usage, process count |
| Prometheus metrics | `GET /1.0/metrics` | OpenMetrics text for all instances in one request — ideal for the global dashboard |
| Lifecycle | `PUT /1.0/instances/{name}/state` (`UpdateInstanceState`, action `start|stop|restart|freeze|unfreeze`) | Async: returns an **Operation** → `op.WaitContext(ctx)` |
| Create | `POST /1.0/instances` (`CreateInstance`) | Image source (local alias or remote like `images:debian/12`), profiles, config, devices |
| Delete | `DELETE /1.0/instances/{name}` | Must be stopped first (or stop+delete flow) |
| Import | `POST /1.0/instances` with backup tarball (`CreateInstanceFromBackup`); `POST /1.0/images` (image import) | Long-running operations with progress → progress bar in UI |
| Logs | `GET /1.0/instances/{name}/console` (console log, containers), `GET /1.0/instances/{name}/logs/{file}`; `/1.0/events?type=logging` for daemon logs | No native "follow stdout" like Docker; console log is polled or attached via WebSocket |
| Events | `GET /1.0/events` WebSocket (`GetEvents` → `AddHandler`) | Types: `lifecycle`, `operation`, `logging` |
| Networks / storage / profiles | `GET /1.0/networks`, `/1.0/storage-pools`, `/1.0/profiles` | Feeds Tree view: project → network → instance, plus profiles |

### 2.3 Normalisation

Both engines are mapped to a common, engine-agnostic domain model (Instance, Network,
Volume, Sample, LogLine, Event). Engine-specific data (Docker labels/healthcheck,
Incus profiles/projects/VM type) is kept in a typed `Extra` section rendered by
engine-specific panels. Detailed in `02-architecture.md`.

---

## 3. Additional Research Points

### 3.1 Permissions and Security

| Engine | Access mechanism | Privilege level |
|---|---|---|
| Docker rootful | User in `docker` group → RW on `/var/run/docker.sock` | **Root-equivalent** (can mount `/` into a container) |
| Docker rootless | Per-user daemon, socket in `$XDG_RUNTIME_DIR` | User-level; recommended where possible |
| Incus full | User in `incus-admin` group → `/var/lib/incus/unix.socket` | **Root-equivalent** |
| Incus restricted | User in `incus` group → `unix.socket.user` (`incus-user` daemon) | Confined to the per-user project `user-<uid>`; no privileged containers or host-path devices |

Design rules:
1. viewDI **never** runs as root, never calls `sudo`, and is never installed setuid.
   If launched as UID 0 it prints a warning (allowed but discouraged).
2. Socket discovery order: CLI flag → env (`DOCKER_HOST`, `INCUS_SOCKET`) → config file →
   well-known paths (rootless before rootful for Docker; full before user socket for Incus).
3. Errors are classified before they reach the UI (state machine in
   `02-architecture.md` §4.2):
   - **Permission** (`EACCES`, HTTP 403) at any time, or **socket not found** (`ENOENT`)
     before the first successful connection → **Unavailable** with an actionable hint
     (e.g. `sudo usermod -aG incus-admin $USER && newgrp incus-admin`, or "Incus socket
     not found — is incusd installed and running?"), slow re-probe.
   - **Transient** (`ECONNREFUSED`, timeout, stream EOF, or `ENOENT` after the engine was
     connected once — a stopped daemon may remove its socket) → **Degraded** with fast
     backoff reconnect.
   Never a crash; the other engine keeps working — engines are fully independent.
4. **Capability detection:** with the restricted Incus socket, the UI disables actions
   the daemon will refuse (detected from `/1.0` `auth`/project info) instead of letting
   them fail.
5. **Destructive actions** (delete, force-stop, import overwrite) always require a
   confirmation modal that shows the target name and engine. A `--read-only` flag
   disables all mutating actions globally (safe mode for monitoring).
6. No secrets are written to config. Environment variables shown in Inspect are masked
   by default when the key matches `(?i)(pass|secret|token|key)`; reveal per value.

### 3.2 Concurrency and Event Streaming

**Principle:** the Bubble Tea `Update` loop is the only place where UI state changes,
and it must never perform I/O. All daemon I/O happens in goroutines owned by providers.

![Concurrency and event streaming](img/concurrency.png)

*Generated by `docs/diagrams/generate.py` (`make docs-diagrams`).*

Strategy:
1. **Event-driven listings.** Initial full list, then incremental updates from
   Docker `/events` and Incus `/1.0/events` (lifecycle). A periodic full resync
   (default 30 s) corrects drift from missed events.
2. **Message pump.** Each provider exposes a channel; a re-arming `tea.Cmd` blocks on
   it and returns the next message (standard Bubble Tea pattern; testable, no
   global `Program.Send`). High-frequency sources are **coalesced** before reaching the UI.
3. **Metrics: hybrid sampling.**
   - Dashboard / table: read cgroup v2 files directly (`/sys/fs/cgroup/.../cpu.stat`,
     `memory.current`, `io.stat`) for all containers in one pass per tick. The cgroup
     path is resolved once per container (cached until restart), preferably from
     `/proc/<init-pid>/cgroup`, with known layouts as fallback:
     - Docker rootful, systemd driver: `system.slice/docker-<id>.scope`
     - Docker rootful, cgroupfs driver: `docker/<id>`
     - Docker rootless: under the user's systemd manager,
       `user.slice/user-<uid>.slice/user@<uid>.service/…/docker-<id>.scope`
     - Incus containers: `lxc.payload.<name>` in the `default` project,
       `lxc.payload.<project>_<name>` in any other project.
     Falls back to Incus `/1.0/metrics` and Docker one-shot stats if cgroups are not
     readable. Network I/O from Incus state / Docker stats.
   - **Incus VMs are never sampled through cgroups**: their cgroup only measures the
     host-side QEMU process (guest page cache and ballooning make it misleading). VM
     metrics come from the Incus API (`/1.0/metrics`, instance state); guest memory and
     disk usage need `incus-agent` running in the VM, otherwise those fields are shown
     as unavailable rather than guessed.
   - Detail tab: open a dedicated stream (Docker `stats?stream=1`, Incus `state` polling
     at 1 s) only for the container being viewed; cancelled when the tab closes.
4. **Timeouts everywhere.** Every request uses `context.WithTimeout` (default 5 s;
   long-running operations use their own context with progress). A hung socket can
   delay one goroutine, never the UI.
5. **Backpressure.** Channels are bounded; metric samples overwrite the latest value
   (lossy by design), log lines go into a bounded ring buffer (default 10 000 lines per
   tab), events are never dropped (small volume).
6. **Lifecycle operations** run as background jobs with an ID; the UI shows a job list /
   spinner and receives `JobProgress` / `JobDone` messages.

### 3.3 Configuration Persistence (XDG)

| Purpose | Path (default) | Format |
|---|---|---|
| User config | `$XDG_CONFIG_HOME/viewDI/config.toml` → `~/.config/viewDI/config.toml` | TOML |
| User themes | `~/.config/viewDI/themes/*.toml` (override/extend built-ins) | TOML |
| Keybinding overrides | `~/.config/viewDI/keys.toml` (optional) | TOML |
| UI state (last view, open tabs, column widths, sort, selected project) | `$XDG_STATE_HOME/viewDI/state.json` → `~/.local/state/viewDI/state.json` | JSON (machine-written) |
| Debug log | `~/.local/state/viewDI/viewdi.log` (rotated, opt-in via `--debug`) | text/slog |
| Cache (image lists, remote image index) | `$XDG_CACHE_HOME/viewDI/` → `~/.cache/viewDI/` | JSON |

Rules: config is human-edited and never rewritten by the app except through an explicit
"save settings" action; state is app-owned and freely rewritten (atomic write: temp file +
rename). Built-in themes are embedded in the binary (`go:embed`). Missing or invalid config
falls back to defaults with a warning toast — never a startup failure.

Theme precedence: `--theme` flag > last theme confirmed in the picker (`state.json`) >
`[ui] theme` in `config.toml` > `everforest-dark`. The picker only persists on confirm
(enter), never while previewing.

Built-in themes (initial set): Everforest (dark/light), Dracula, Catppuccin (Mocha/Latte),
Monokai, Gruvbox, Nord, Tokyo Night. Themes define **semantic tokens**, not widget colors
(see `02-architecture.md` §6).

### 3.4 Update Discovery and Notifications

viewDI detects two kinds of updates and notifies the user. It **never installs anything**:
it runs unprivileged, so engine upgrades are shown with the exact command for the detected
distro, and image updates are applied only through explicit user actions.

![Update checker](img/updates.png)

#### A. Engine updates (Docker Engine, Incus)

| Step | Docker | Incus |
|---|---|---|
| Installed version | `GET /version` (`Version`, `ApiVersion`) | `GET /1.0` → `environment.server_version` |
| Channel | stable | **LTS (6.0.x)** vs **feature releases (6.x)** — auto-detected from installed version, overridable |
| Source `package` (default when detectable) | Read-only query of the package manager: `apt-cache policy docker-ce`, `dnf check-update docker-ce`, `pacman -Qu`, `zypper list-updates` | same for `incus` package (Zabbly repo, distro repos) |
| Source `upstream` | GitHub releases of `moby/moby` (`docker-v*` tags) | GitHub releases of `lxc/incus` |

`auto` mode prefers the package manager — it's the truth for "what can I actually install" —
and falls back to upstream when the package isn't managed (e.g. static binaries).
Upstream requests use conditional `If-None-Match` (ETag) against the cached response to
stay well inside the unauthenticated GitHub rate limit (60 req/h); an optional
`GITHUB_TOKEN` env var is honoured but never stored.

Result shown: `installed → available`, release date, link to release notes, security flag
when the release notes / package changelog mention a CVE, and the upgrade hint
(e.g. `sudo apt install --only-upgrade docker-ce`).

#### B. Image updates (per container / instance)

| Engine | Detection | Action offered |
|---|---|---|
| Docker | Compare local image `RepoDigests` with the registry manifest digest via `GET /distribution/{ref}/json` (the daemon performs registry auth, so private registries work with the user's existing `docker login`). Skipped for images without a registry reference (locally built / imported). | **Pull** new image. For Compose-managed containers: show `docker compose -p <project> up -d <service>` hint. For standalone containers: optional **Recreate** (same config, new image) behind a confirm modal showing the diff. |
| Incus | Compare the instance's `volatile.base_image` fingerprint with the current fingerprint of its source alias on the remote (simplestreams, e.g. `images:debian/12`), using the Incus client's simplestreams connector. Cached local images with `auto_update` are reported as informational. | **Refresh cached image**. `incus rebuild` is offered only with a strong warning (it wipes the root filesystem). |

Digest checks are throttled (max 4 concurrent registry queries, results cached per image
reference, not per container).

#### C. Scheduling and configuration

```toml
[updates]
enabled          = true        # master switch (no network calls when false)
check_on_startup = true        # runs 30 s after startup, never blocks the UI
engine_interval  = "24h"
image_interval   = "6h"
engine_source    = "auto"      # auto | package | upstream
incus_channel    = "auto"      # auto | lts | stable
check_images     = true
ignore_images    = ["postgres:*", "registry.local/*"]   # glob patterns
notify_desktop   = false       # D-Bus desktop notification (opt-in)
notify_min_level = "minor"     # patch | minor | major | security
```

Manual check: `U` (anywhere) or "Check now" in the Updates view. Results and "last seen"
markers are cached in `~/.cache/viewDI/updates.json`; "snooze"/"ignore this version"
decisions are stored in `~/.local/state/viewDI/state.json`.

#### D. Notification channels

1. **Header badge** — `⬆ 3` next to the engine status (always visible).
2. **Admin dashboard alerts** — one alert per engine update; aggregated alert for image
   updates ("5 images have updates").
3. **Table / Tree markers** — `⬆` next to instances whose image has an update.
4. **Toast** — only for *newly discovered* updates (diff vs last seen), never repeated.
5. **Updates view** (`6`) — full list with actions: pull / recreate / refresh / snooze /
   ignore version / open release notes.
6. **Desktop notification** (opt-in) — via D-Bus `org.freedesktop.Notifications`,
   `notify-send` fallback; respects `notify_min_level`.
