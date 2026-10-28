# viewDI — TUI Mockups (structural proposal)

Status: **Draft for validation** · Target minimum terminal size: 100×30 (responsive down to 80×24).

Global layout: header (engine status + view tabs) · body · status/key-hint bar.

## Global key map (proposal)

| Key | Action | Key | Action |
|---|---|---|---|
| `1`–`6` | Admin / Resources / Table / Tree / Jobs / Updates | `enter` | Open detail |
| `U` | Check for updates now | `J` | (Tree) jump to next occurrence |
| `tab` / `shift+tab` | Next / prev tab or pane | `esc` | Back / close modal |
| `/` | Filter | `e` | Cycle engine filter (all/docker/incus) |
| `s` / `x` / `R` | Start / Stop / Restart | `d` | Delete (confirm) |
| `n` | New container (form) | `i` | Import |
| `p` | Pause/resume (logs) | `P` | Incus project selector |
| `T` | Theme picker | `?` | Help overlay |
| `ctrl+r` | Force reconnect/resync | `q` | Quit |

## M1 — Admin Dashboard (`1`)

```
┌ viewDI ─────────────────────────────────────── ● Docker 29.x  ● Incus 6.x  ⬆ 4 ┐
│ [1 Admin] 2 Resources  3 Table  4 Tree  5 Jobs  6 Updates            theme: Everforest │
├──────────────────────────────────────────────────────────────────────────────────────┤
│ ┌ Docker daemon ───────────────────────┐ ┌ Incus daemon ─────────────────────────────┐│
│ │ Status    ● connected  (rootful)     │ │ Status    ● connected  (incus-admin)      ││
│ │ Version   29.x   API 1.5x            │ │ Version   6.x    project: default         ││
│ │ Containers  12  ▶ 9  ■ 3  ⏸ 0        │ │ Instances    7  ▶ 5 (2 VM)  ■ 2           ││
│ │ Images 34   Volumes 18   Networks 6  │ │ Images 9  Pools 2  Networks 3  Profiles 4 ││
│ │ Storage driver overlay2  cgroup v2   │ │ Storage zfs (pool0), dir (local)          ││
│ └──────────────────────────────────────┘ └───────────────────────────────────────────┘│
│ ┌ Alerts (4) ────────────────────────────────────────────────────────────────────────┐│
│ │ ⬆ incus   engine 6.0.5 → 6.0.6  (LTS · security fix)  — 6 to view                    ││
│ │ ▲ docker  postgres-dev   unhealthy for 4m (healthcheck failing)                    ││
│ │ ▲ incus   k3s-node1      memory 92% of limit (3.7/4.0 GiB)                          ││
│ │ ● docker  nginx-proxy    restarted 3 times in 10m                                   ││
│ └────────────────────────────────────────────────────────────────────────────────────┘│
│ ┌ Shortcuts ─────────────────────────────┐ ┌ Recent events ──────────────────────────┐│
│ │ n  new container    i  import          │ │ 14:02:11 docker start   redis-cache     ││
│ │ 3  all containers   4  network tree    │ │ 14:01:57 incus  stop    test-alpine     ││
│ │ T  change theme     ?  help            │ │ 13:58:03 docker pull    nginx:1.27      ││
│ └────────────────────────────────────────┘ └─────────────────────────────────────────┘│
├──────────────────────────────────────────────────────────────────────────────────────┤
│ ? help  1-5 views  / filter  T theme  q quit                        jobs: 0 running  │
└──────────────────────────────────────────────────────────────────────────────────────┘
```

## M2 — Resources Dashboard (`2`)

```
┌ viewDI ─────────────────────────────────────────────── ● Docker  ◐ Incus (reconnecting 4s) ┐
│ 1 Admin [2 Resources] 3 Table  4 Tree  5 Jobs                                  refresh 1s │
├ ⚠ Incus socket not responding — showing data from 14:03:10 · retry in 4s · ctrl+r now ────┤
│ ┌ DOCKER ─────────────────────────────────────┐ ┌ INCUS (stale) ─────────────────────────┐│
│ │ CPU  ███████░░░░░░░░░  38%  ▁▂▃▅▆▅▃▂▃▅▆▇    │ │ CPU  ███░░░░░░░░░░░  17%  ▁▁▂▂▃▂▁▂▂▃   ││
│ │ RAM  ██████████░░░░░░  6.1/16.0 GiB  ▃▃▄▄▅  │ │ RAM  █████░░░░░░░░░  4.9/16.0 GiB ▂▂▃▃ ││
│ │ NET  ↓ 2.4 MB/s  ↑ 310 KB/s   ▁▃▇▅▂▁▂▄      │ │ NET  ↓ 120 KB/s  ↑ 80 KB/s   ▁▁▂▁▁▂    ││
│ │ DISK r 12 MB/s   w 4.1 MB/s   ▂▂▅▃▁▁▂▂      │ │ DISK r 1 MB/s    w 0.3 MB/s  ▁▁▁▂▁▁    ││
│ └─────────────────────────────────────────────┘ └────────────────────────────────────────┘│
│ ┌ Top consumers ─────────────────────────────────────────────────────────────────────────┐│
│ │ ENG    NAME             CPU%   MEM            NET ↓/↑          BLK r/w                 ││
│ │ dock   postgres-dev     21.4   1.2 GiB        40K / 12K        8.0M / 3.1M             ││
│ │ incus  k3s-node1        14.9   3.7 GiB        110K / 70K       0.9M / 0.2M             ││
│ │ dock   nginx-proxy       6.2   48 MiB         2.3M / 290K      0 / 0                   ││
│ └────────────────────────────────────────────────────────────────────────────────────────┘│
├───────────────────────────────────────────────────────────────────────────────────────────┤
│ enter open  s sort  +/- refresh rate  ctrl+r reconnect                                     │
└───────────────────────────────────────────────────────────────────────────────────────────┘
```

## M3 — Table View (`3`)

```
┌ viewDI ──────────────────────────────────────────────────────── ● Docker  ● Incus ┐
│ 1 Admin  2 Resources [3 Table] 4 Tree  5 Jobs        engine: all  filter: /web_     │
├─────────────────────────────────────────────────────────────────────────────────────┤
│   ENG    NAME              KIND  STATE      IMAGE                  CPU%  MEM    UP  │
│ ▶ dock   web-frontend      ctr   ● running  node:22-alpine          3.1  210M   2d  │
│   dock   web-api           ctr   ● running  ghcr.io/me/api:1.4      7.8  340M   2d  │
│   dock   web-db            ctr   ▲ unhealthy postgres:17           21.4  1.2G   2d  │
│   incus  web-lab           ctr   ● running  images:debian/12        0.4   88M   6h  │
│   incus  web-win11         vm    ■ stopped  (local) win11            —     —    —   │
│                                                                                     │
│                                                                    5 of 19 shown    │
├─────────────────────────────────────────────────────────────────────────────────────┤
│ enter detail  s start  x stop  R restart  d delete  n new  i import  e engine  / filter │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

## M4 — Interactive Tree View (`4`)

A real navigable tree: box-drawing branch guides, a cursor (`▌` + highlighted row) that
moves node by node with the arrow keys, and an inspector on the right that follows the
cursor instantly. Interaction model: `02-architecture.md` §5.

### M4.a — Cursor on a container

```
┌ viewDI ─────────────────────────────────────────────────────────── ● Docker  ● Incus  ⬆ 4 ┐
│ 1 Admin  2 Resources  3 Table [4 Tree] 5 Jobs  6 Updates           group: network (g)     │
├ Docker › web_default › web-api ─────────────────┬─────────────────────────────────────────┤
│  viewDI                                          │ ● web-api                    docker ctr │
│  ├── 🐳 Docker 29.x ················ 12 ▶9 ■3    │ ─────────────────────────────────────── │
│  │   ├── ◆ bridge          172.17.0.0/16         │ State    ● running · up 2d 4h           │
│  │   │   └── ● redis-cache                       │ Image    ghcr.io/me/api:1.4  ⬆ 1.5      │
│  │   ├── ◆ web_default     172.20.0.0/16         │ Compose  web / api                      │
│  │   │   ├── ● web-frontend                      │ IPs      web_default  172.20.0.3        │
│ ▌│   │   ├── ● web-api  ⬆  ⇄                    │          monitoring   172.21.0.5        │
│  │   │   │   ├── ⇢ 8080 → 8080/tcp               │ Ports    0.0.0.0:8080 → 8080/tcp        │
│  │   │   │   ├── ▣ ./config → /app/config  ro    │ Mounts   ./config → /app/config (ro)    │
│  │   │   │   └── ⇄ also on: monitoring           │ Health   ● healthy (12s ago)            │
│  │   │   └── ▲ web-db   unhealthy                │                                         │
│  │   └── ◆ monitoring (2) ▸                      │ CPU  7.8%  ▁▂▃▅▃▂▂▃▅▆▅▃                 │
│  └── ▦ Incus 6.0.5 · project default ··· 7 ▶5 ■2 │ MEM  340M/1G ▃▃▄▄▅▅▅▅▅▅▅                │
│      ├── ◆ incusbr0        10.10.0.1/24  managed │ NET  ↓40K ↑12K/s                        │
│      │   ├── ● web-lab                           │                                         │
│      │   └── ● k3s-node1  vm                     │ enter details · s start · x stop        │
│      └── ◆ macvlan0 ▸                            │ R restart · d delete · J next ⇄         │
├──────────────────────────────────────────────────┴─────────────────────────────────────────┤
│ ↑↓ move  ← parent/collapse  → expand/child  [ ] sibling  space toggle  * all  / filter  g group │
└────────────────────────────────────────────────────────────────────────────────────────────┘
```

### M4.b — Cursor on a network (inspector adapts to the node type)

```
├ Incus › incusbr0 ───────────────────────────────┬─────────────────────────────────────────┤
│  ...                                             │ ◆ incusbr0                 incus network│
│  └── ▦ Incus 6.0.5 · project default             │ ─────────────────────────────────────── │
│ ▌    ├── ◆ incusbr0        10.10.0.1/24  managed │ Type     bridge (managed)               │
│      │   ├── ● web-lab                           │ IPv4     10.10.0.1/24  NAT on           │
│      │   └── ● k3s-node1  vm                     │ IPv6     fd42:7a1::1/64                 │
│      └── ◆ macvlan0 ▸                            │ DHCP     on · 2 leases                  │
│                                                  │ Members  web-lab, k3s-node1             │
│                                                  │ Traffic  ↓ 150K/s  ↑ 82K/s  ▁▂▂▃▂▁      │
```

### M4.c — Filter (`/k3s`): matches stay visible with their ancestors

```
│  viewDI                                filter: k3s_  (1 match)  │
│  └── ▦ Incus 6.0.5 · project default                            │
│      └── ◆ incusbr0                                             │
│ ▌        └── ● k3s-node1  vm                                    │
```

Visual rules:
- Branch guides (`├──`, `└──`, `│`) use the `tree_guide` token (muted); the cursor row uses
  `tree_cursor_bg` across the full width of the tree pane.
- Collapsed nodes show `▸` plus a child count; status dots use `ok` / `warn` / `err`.
- `⬆` marks an available image update, `⇄` a multi-network instance.
- Icons fall back to ASCII (`[D]`, `[I]`, `*`, `#`) when no Nerd Font/emoji support is
  detected or `ascii_icons = true` is set.

## M5 — Detail View: tabs

```
┌ viewDI › web-api (docker) ────────────────────────────────────────── ● running  up 2d ┐
│ [Overview] Logs  Inspect  Network  Volumes  Resources                                   │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│ Description   REST API for the web stack                (label: org.opencontainers.     │
│                                                          image.description)             │
│ Image         ghcr.io/me/api:1.4   sha256:9f3c…a1                                       │
│ Command       /app/server --port 8080                                                   │
│ Created       2026-10-26 11:04   Restart policy  unless-stopped   Health ● healthy      │
│ Compose       project web · service api                                                 │
│ Labels        com.docker.compose.project=web   tier=backend   owner=homelab             │
│ Env           PORT=8080  DB_HOST=web-db  DB_PASSWORD=•••••• (v reveal)                  │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│ tab next  s start  x stop  R restart  d delete  esc back                                 │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

Logs tab:

```
│ Overview [Logs] Inspect  Network  Volumes  Resources        ⏸ PAUSED (+142 new)  filter: /error_ │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│ 14:02:10.331 out  GET /health 200 0.4ms                                                    │
│ 14:02:11.902 err  ERROR db: connection refused (web-db:5432)                               │
│ 14:02:12.004 err  ERROR retrying in 2s                                                     │
│ ...                                                                                        │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│ p pause/resume  / filter (regex)  w wrap  t timestamps  1 stdout 2 stderr  G end  gg top  │
```

Resources tab:

```
│ Overview  Logs  Inspect  Network  Volumes [Resources]                       window: 5m (+/-) │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│ CPU  7.8%  (limit 2 cpus)        ▁▂▃▂▂▃▅▇▆▅▃▂▂▃▅▆▅▃▂▁▂▃▃▂▂▃▅▆▇█▇▆▅▃                       │
│ MEM  340 MiB / 1 GiB (33%)       ▃▃▃▃▄▄▄▄▄▄▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅                       │
│ NET  ↓ 40 KB/s  ↑ 12 KB/s        ▁▁▂▁▁▃▁▁▂▁▁▁▂▅▂▁▁▁▂▁▁                                     │
│ BLK  r 0 B/s  w 120 KB/s         ▁▁▁▁▁▁▂▁▁▁▁▁▁▁▁▃▁▁▁▁                                      │
│ PIDS 14                                                                                     │
```

## M6 — Updates View (`6`)

```
┌ viewDI ─────────────────────────────────────────────────────────── ● Docker  ● Incus  ⬆ 4 ┐
│ 1 Admin  2 Resources  3 Table  4 Tree  5 Jobs [6 Updates]     last check 09:12 · U check now │
├────────────────────────────────────────────────────────────────────────────────────────────┤
│ ENGINES                                                                                    │
│   ENG    INSTALLED   AVAILABLE   CHANNEL   SOURCE     SEVERITY      UPGRADE HINT           │
│ ▶ incus  6.0.5       6.0.6       LTS       apt        ⚠ security    sudo apt install --only-upgrade incus │
│   docker 29.1.2      29.1.2      stable    apt        ✓ up to date                         │
│                                                                                            │
│ IMAGES                                                                                     │
│   ENG    INSTANCE(S)              IMAGE                    LOCAL      REMOTE     ACTION    │
│   dock   web-api                  ghcr.io/me/api:1.4       sha…9f3c   sha…a71e   pull/recreate │
│   dock   nginx-proxy              nginx:1.27               sha…11b0   sha…c3d2   pull/recreate │
│   incus  web-lab                  images:debian/12         4f1a…      8be0…      refresh   │
│   dock   web-db (ignored)         postgres:17              —          —          ignored   │
├────────────────────────────────────────────────────────────────────────────────────────────┤
│ enter notes  p pull  c recreate  f refresh  z snooze 7d  i ignore version  U check now     │
└────────────────────────────────────────────────────────────────────────────────────────────┘
```

Toast on newly found updates (bottom-right, auto-dismiss 6 s):

```
                                                   ┌ ⬆ Updates found ───────────────┐
                                                   │ Incus 6.0.6 (security)         │
                                                   │ 2 Docker images · 6 to view    │
                                                   └────────────────────────────────┘
```

## M7 — Modals

```
┌ Delete container? ──────────────────────────┐   ┌ Theme ───────────────────┐
│ Engine:  docker                             │   │   Everforest Dark        │
│ Name:    web-db                             │   │ ▶ Catppuccin Mocha  ■■■■ │
│ [x] remove anonymous volumes                │   │   Dracula                │
│ [ ] force (container is running)            │   │   Monokai                │
│                                             │   │   Gruvbox / Nord / Tokyo │
│ Type the name to confirm: web-d_            │   │ live preview · enter set │
│        [ Cancel ]   [ Delete ]              │   └──────────────────────────┘
└─────────────────────────────────────────────┘
```

New-container form: engine selector → fields adapt (Docker: image, name, ports, env,
volumes, network, restart policy · Incus: image source/alias, name, type ctr/vm,
profiles, CPU/memory limits, network). Import: file picker + type (Docker image tarball,
Docker rootfs import, Incus backup, Incus image) with a progress bar in Jobs.
