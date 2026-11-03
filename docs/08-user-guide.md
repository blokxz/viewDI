# User guide

Everything the README does not cover: the keys of each view, how metrics and update checks work, and how the daemons are found. The complete key map, with mockups, is in [`03-mockups.md`](03-mockups.md); press `?` inside viewDI for the live bindings.

## Views and keys

Four tabs, switched with `1`–`4`: **Home**, **Machines**, **Library** and **Activity**.
`tab` cycles the panes of a view, `T` opens the theme picker (live preview), `?` shows every
binding (and the ones of the view you are in), `q` quits. `n` creates a container or VM, `i`
imports one, `U` checks for updates from any view. `--read-only` disables every action that
changes something. The full key map, with mockups, is in `docs/03-mockups.md`.

### Home (`1`)

One sentence says how things are ("Everything that should be running is running", "2 things
need you", "Incus cannot be reached…"). **Needs you** lists what is wrong in words, each item
with the key that fixes it: `s` start it, `r` restart, `L` logs, `f` it is fine (stop
reporting this stop), `a` upgrade the engine (shows the command first), `z` later (snooze an
update), `v` see it in Activity. `enter` does the first action of the item. **Apps** shows one
card per app: a compose project, a lab, a library entry, or "other machines" for everything
else. `enter` opens the app's machines in Machines; `d` cycles the density (cards, rows,
mosaic: chosen automatically for the number of apps, and remembered once you change it), `p`
pins an app to the top, `/` filters. The strip at the bottom is the usage of both engines;
`u` expands it into the full charts (`o`/`O` sort the top consumers, `+`/`-` change the
refresh rate; `u` goes back).

"Stopped on purpose" is a guess: a machine viewDI saw running that stops without a viewDI job
(or a Docker container that exits with a non-zero code) counts as *down*; anything that was
already stopped when viewDI started counts as *off*. A stop typed in another terminal looks
the same as a crash, so `f` tells viewDI it was fine.

### Machines (`2`)

Every machine of both engines. By default a tree grouped by app (app → machines); `v` flips
to the flat list. In the tree: `↑↓`/`jk` move, `→`/`l` open or go to the first child,
`←`/`h` close or go to the parent, `[`/`]` siblings, `space` folds, `*`/`-` open all below /
close all, `g` cycles the grouping (app, network, project, compose, engine only, lab), `J`
jumps to the other networks of a machine, `/` filters (name, image, network, IP) with
`ctrl+n`/`ctrl+p` between matches. The panel on the right describes the selected row in
plain words: how it is doing, how to reach it, what changed, what it needs. In the list:
`/` filter, `e` engine filter, `o`/`O` sort, `P` Incus project, `space` marks rows.
`L` opens the lab menu: create a lab, add machines of either engine, link them (a dot runs
along a link while both ends run, faster with more traffic), unlink, rename or delete a lab.
Labs never touch the daemons, so they work under `--read-only`. `ctrl+r` reconnects.

Actions: `s` start, `x` stop, `R` restart, `F` pause/resume, `d` delete, `b`/`B` open in the
browser — on the rows marked with `space`, or the selected machine; the key bar at the
bottom shows only the ones the selected row supports. `enter` opens the Detail view:
`tab`/`shift+tab` switch its tabs, `esc` goes back; in Logs `p` pauses, `/` filters (regex),
`S` cycles stdout/stderr, `w` wraps, `G`/`gg` jump; in Inspect `←`/`→` fold, `/` searches,
`y` copies the path; in Overview `v` reveals a masked value. `n` creates a container or VM:
a form with local image suggestions (`ctrl+n`/`ctrl+p`), registry or `images:` search
(`ctrl+f`), ports, environment, volumes, limits and more. `i` imports a Docker image or
rootfs tarball, an Incus backup or an Incus image through a file picker.

### Activity (`4`)

One feed instead of separate Jobs, Updates, alerts and events screens. **NOW** holds what
needs attention or is in progress: active alerts, running jobs with their progress, and
available updates; **TODAY** and **EARLIER** hold the history, newest first, including what
happened on the daemons outside viewDI ("started", "ran out of memory"). `←`/`→` change the
filter chip (everything, alerts, updates, jobs, history). Each line offers its own keys:
`c` cancels a running job; on an engine update `enter` opens the release notes, `u` upgrades
the engine, `z` snoozes it for 7 days and `I` ignores this version (press again to undo);
on an image update `p` pulls a Docker image, `f` refreshes an Incus image (it only downloads the
build: instances that exist keep their root disk, so the row stays until you rebuild), `R` recreates the
Docker containers of the image on the new build after showing what is kept (name, env,
ports, mounts, networks, restart policy, labels) — if anything fails the original container
gets its name back and runs again. Compose containers get the `docker compose up -d` hint
instead. On an Incus row `R` **rebuilds** the instances from the newest build: it replaces the root
disk (config, devices and attached volumes stay), so you type the instance's name first; a snapshot
`viewdi-pre-rebuild-<date>` is taken before anything changes (no snapshot, no rebuild), the instance is
stopped, rebuilt and started again if it ran, and a failure at any step restores the snapshot. The
snapshot is kept: delete it with `incus snapshot delete <name> <snapshot>` when you are sure. On an alert or a daemon event `enter` opens the machine.

### Doing things without remembering keys (`ctrl+k`, `enter`)

`ctrl+k` opens the **command palette** from any view: type a few letters of a machine
("web"), a verb and a machine ("restart redis"), a library entry ("postgres"), a view
("activity") or a setting ("theme"). Results are fuzzy-matched, the ones you ran lately come
first and every result shows its own key, so the palette also teaches the keys. `--read-only`
hides what changes something.

`enter` on a machine opens an **action menu** with the verbs that make sense for it right now:
open in the browser, open a shell, read the logs, restart, stop, pause, update the image,
details, delete. Each shows its direct key, which keeps working. `→` in the flat list opens the
details. If you prefer `enter` to open the Detail view straight away, set `enter = "details"`
under `[ui]` in `config.toml`.

`E` opens a **shell** in the selected running machine (`docker exec -it` / `incus exec`, bash
when the image has it, else sh); viewDI hands the terminal over and takes it back when you
exit. It is off in `--read-only`. `m` **raises the memory or CPU limit** of the selected running
machine (from a memory or CPU alert too, in the Home and Activity): a form with a suggested
value, then a confirmation; the change is live and nothing restarts. Incus takes whole CPUs.

### Old keys

The numbered views of earlier releases (1 Admin, 2 Resources, 3 Table, 4 Tree, 5 Jobs,
6 Updates, 7 Library) were folded into these four. A `keys.toml` that rebinds the old
actions keeps working and prints a warning that names the new action: `view_admin` and
`view_resources` are now `view_home`, `view_jobs` and `view_updates` are `view_activity`,
and the `[view.jobs]` and `[view.updates]` tables moved to `[view.activity]` (the sort keys
of the old Updates view no longer exist; the feed is ordered by urgency). The last view
saved in `state.json` is mapped the same way. `--mock=chaos` cycles through daemon failures,
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

## The library (`3`)

A catalog of well-known Docker images and Incus systems. Browsing reads only the catalog
and what the daemons already have locally; nothing touches the network.

| Key | Action |
|---|---|
| `↑`/`↓`, `tab` | move; `tab` goes to the categories and to the details |
| `/`, `e`, `esc` | filter by name, image, category and description; Docker/Incus filter; clear |
| `enter` | run the entry in the create form of its engine, prefilled |
| `S` | search Docker Hub (through your Docker daemon) and run a result |
| `x` | list the local images no instance uses, with sizes, and remove one after a confirmation |

Running an entry proposes a free name (`postgres`, `postgres-2`…), host ports published on
`127.0.0.1` only (moved to the next free port when taken, with a note), generated passwords
(masked in the form), named volumes, and labels the instance with the entry (so the view can
show `● N up`). When the job succeeds a dialog shows the entry's note, for example how to
connect, with the real name and port. In the form, `ctrl+d` asks how big the download is
(Docker Hub images; Incus asks on its own); an image you already have says "no download".
Everything is off in `--read-only`.

Your own entries go in `~/.config/viewDI/library/*.toml`, in the same format as the built-in
ones (`library/` in the repository). One with a built-in `id` replaces it; an invalid file is
skipped with a warning. Docker entry:

```toml
[[entry]]
id = "my-app"
name = "My app"
category = "Mine"
description = "My internal service."
engine = "docker"
image = "registry.example.com/team/app:2.4"   # a pinned tag, never latest
note = "Open http://localhost:{{port 8080}} ({{name}})."
[[entry.ports]]
container = 8080
host = 8090
[[entry.env]]
name = "APP_SECRET"
generate = "password"
[[entry.volumes]]
name = "data"
path = "/var/lib/app"
```

An Incus entry uses `engine = "incus"`, an alias on the `images:` remote, `kinds = ["container", "vm"]`,
`default_kind`, `cpu`, `memory`, `profiles` and, on a `/cloud` image, a `cloud_init` preset that starts
with `#cloud-config`.

## Configuration reference

| File | Purpose |
|---|---|
| `~/.config/viewDI/config.toml` | settings (`[ui]`, `[tree]`, `[docker] host`, `[incus] socket`, `[alerts]`, `[logs]`, `[actions]`, `[updates]`, `[browse]`), never rewritten by the app |
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

[browse]
ports = [9090, 8006, 8080, 3000]  # extra web ports B offers on every address
                                  # (Cockpit, Proxmox…); [] offers none; at most 16
```

### Opening an instance's web interface (`b`, `B`)

`b` opens the best address of the selected instance and `B` lists them all. Besides the published
ports and `http`/`https` on its addresses, `B` offers the `[browse] ports` on each address. When
the interface is somewhere else, name it on the instance; `{ip}` stands for its first IPv4
address:

```sh
incus config set NAME user.viewdi.url 'https://{ip}:9090/system'
docker run --label 'viewdi.url=https://{ip}:9090/system' ...
```

`b` then opens that address first. Only `http` and `https` are accepted, and the browser is still
only sent to private, loopback, link-local or CGNAT IPv4 addresses and `localhost`, whatever the
label says.

Secret-looking environment values (`*PASSWORD*`, `*TOKEN*`, `*_KEY`…) are masked in the
Overview and Inspect tabs; `v` reveals one at a time in Overview.

Built-in themes: Everforest (dark/light), Dracula, Catppuccin (Mocha/Latte), Monokai,
Gruvbox, Nord, Tokyo Night.
