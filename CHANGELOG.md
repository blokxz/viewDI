# Changelog

All notable changes to viewDI. The full notes of each release are on the
[releases page](https://github.com/blokxz/viewDI/releases). Versions follow
[Semantic Versioning](https://semver.org/); before `1.0.0` a minor version could change anything.

## [Unreleased]

### Added (error log)
- `~/.local/state/viewDI/viewdi.log` is now written on every run, not only with `--debug`: warnings and
  errors, every warning or error toast shown in the corner (with its full text) and every job that
  failed, with the daemon's message. Each line carries the viewDI version. `--debug` still adds the
  debug level; `--mock` writes no log unless `--debug` is given. The file is `0600` and rotates to
  `viewdi.log.1` above 10 MiB.

### Added (Docker Hub browser)
- `S` in the Library opens a Docker Hub browser with three tabs: **Search**, **Most downloaded** (the
  official images by pulls) and **Favourites** (`ctrl+f` marks one; kept in `state.json`, at most 100).
  Results show official, stars and downloads; `enter` runs the image through the create form.
- `internal/hub`, a small client for hub.docker.com: https to that host only (a redirect to another
  host is refused), 1 MiB cap, names checked against the Docker name grammar, descriptions sanitised,
  parsers fuzzed. It is asked only when you search or open Most downloaded; never in `--mock` (a sample
  list) or with `[updates] enabled = false` (Search then asks the Docker daemon, as before).
- Security review finding 25.

### Fixed (Incus rebuild, copy devices)
- A rebuild with a copy failed on an instance with a static address ("IP address ... already defined on
  another NIC"): the copy claimed what the rebuilt instance needed to start. The copy now holds only the
  root disk and the NICs without `ipv4.address`, `ipv6.address` and `hwaddr`; the original devices are
  saved in its `user.viewdi.devices` key and put back if the copy has to replace the original. A custom
  volume, a proxy port or a GPU would have clashed the same way.
- A copy the pool cannot hold is refused before anything is touched (pools that copy on write are not
  checked, nor an instance whose usage the daemon does not report).

### Added (Incus rebuild, copy devices)
- The Home lists a copy whose instance is gone ("web is gone and its data is in this copy") with the
  `incus rename` command, so a rollback that could not finish is not missed.
- The rebuild dialog says the SSH host keys are lost (`ssh-keygen -R <address>`).

### Fixed (Incus rebuild)
- `R` on an Incus image update failed on a real Incus with "Cannot remove an instance volume that has
  snapshots": a rebuild deletes the root volume, which Incus refuses when it has snapshots, and the
  snapshot the rebuild took first was one. The safety net is now a **copy** of the stopped instance
  (`<name>-pre-rebuild-<date>`, no snapshots), a "Keep a copy" checkbox that is on by default; when a
  step fails the instance is deleted and the copy takes its name back. An instance that already has
  snapshots of its own is refused before anything is touched, with their names. The dialog lists copies
  left by earlier rebuilds; viewDI never deletes a copy. `RecreateOptions` gets `Copy`.

### Added (Incus rebuild)
- `R` on an Incus image update rebuilds the instances that run it from the newest build, through the
  Incus API: you type the instance's name, the instance is stopped, rebuilt and started again. It
  replaces the root disk and keeps config, devices and attached volumes; off in `--read-only`.
  `engine.Recreate` now covers Incus (`RecreateOptions.Source`).

### Changed (Incus refresh)
- After `f` (refresh) an Incus row still says "newer build" because the instance keeps its old root
  disk; the row and the result now say `R rebuilds it`. A rebuilt instance clears its row once the check
  runs again. The result of a Docker pull points at `R`, not `c` (the Activity key since 9a).

### Added (doing things without remembering keys)
- `ctrl+k`, the command palette: fuzzy search over machines, their verbs ("restart", "logs", "open in
  the browser"), library entries, views and settings, recent items first, each with its own key;
  `--read-only` hides what changes something.
- `enter` on a machine opens an action menu with the verbs that make sense for it. `→` in the flat list
  opens the details; `[ui] enter = "details"` keeps the old behaviour of `enter`.
- `E` opens a shell in a running machine (`docker exec -it` / `incus exec`), with a fixed argv and a
  validated ID or name; off in `--read-only`.
- `m` raises the memory or CPU limit of a running machine, from the machine, a memory or CPU alert, the
  Home or Activity; a form with a suggested value and a confirmation. The engine contract gets
  `UpdateLimits` (Docker `ContainerUpdate`, Incus `limits.memory` and `limits.cpu`).

### Changed (simpler navigation)
- Four tabs instead of seven: **Home** (`1`), **Machines** (`2`), **Library** (`3`) and **Activity** (`4`).
  The Admin dashboard became the Home; the Table and the Tree became one Machines view (a tree grouped
  by app by default, `v` flips to the flat list); Jobs, Updates, alerts and daemon events became the
  Activity feed; the Resources charts live in the Home's usage strip (`u` expands them).
- The Home shows one card per *app* (a compose project, a lab, a library entry, or "other machines"),
  in cards, rows or a mosaic depending on how many there are (`d` changes it), and a "needs you" list
  in words with the key that fixes each item. A machine that stops without a viewDI job, or a container
  that exits with a non-zero code, is reported; anything already stopped at startup is just "off"
  (`f` says a stop was fine).
- Machines gets a new `g` grouping, **app**, which is the default; the inspector describes an app in plain
  words; the key bar shows only the verbs the selected row supports.
- Activity keys: `c` cancels a job, `p`/`f`/`R` pull, refresh and recreate an image, `u` upgrades an engine,
  `z`/`I` snooze and ignore, `←`/`→` pick the filter. `R` recreates (it was `c` in the old Updates view).
- A `keys.toml` written for the old view actions (`view_admin`, `view_resources`, `view_jobs`,
  `view_updates`, `[view.jobs]`, `[view.updates]`) keeps working and prints a warning naming the new
  action. The last view in `state.json` is mapped to the view that replaced it.

### Added (container library)
- View `7`, the Library: about 30 well-known Docker images and 8 Incus systems (two with a cloud-init
  preset: Debian + Docker, Ubuntu + k3s). `enter` opens the create form filled in with a free name, free
  ports (on `127.0.0.1`), generated passwords, volumes and a label; when the job succeeds a dialog shows the
  entry's note. `/` filters, `e` switches engine; entries are marked `⤓ local` and `● N up`.
- Your own entries under `~/.config/viewDI/library/*.toml` (a built-in `id` is replaced).
- `S` searches Docker Hub (official flag, stars) and runs a result; `ctrl+d` in the create form shows the
  download size (Docker Hub manifest; Incus asks on its own); `x` lists the local images no instance uses and
  removes one after a confirmation.
- Engine contract: `CreateSpec.Config` (Incus instance config such as cloud-init; Docker refuses it),
  `ImageSize`, `RemoveImage`, and stars in image search results. Incus now stores a spec's labels as
  `user.*` keys.

### Added
- `B` also offers the ports of the new `[browse] ports` setting (default 9090, 8006, 8080, 3000: Cockpit,
  Proxmox and common apps) on every address, and `b` opens the address named by the `viewdi.url` Docker label
  or the `user.viewdi.url` Incus key (`https://{ip}:9090/system`). The browser is still only sent to private
  or local addresses.
- `viewdi doctor`: checks the Docker and Incus sockets (owner, mode, whether this session may use
  them), cgroup v2, the terminal, the optional tools and `config.toml`, prints how to fix each
  problem and exits 1 when neither engine is reachable.
- The `.deb`, `.rpm` and AUR packages print a short note after installing: which engine is missing
  and to run `viewdi doctor`.
- `u` in the Updates view upgrades an engine: it shows the exact command (`sudo apt install --only-upgrade
  docker-ce`, `sudo dnf upgrade incus`…), asks, then pauses viewDI and runs it in your terminal, so `sudo`
  prompts for its own password; when it ends the update check runs again. Only those fixed command templates
  run, never through a shell; it is off in `--read-only`, and `--mock` only says what it would run.
- `viewdi --update` (with `--yes` and `--force`): finds the latest release on GitHub and updates this
  installation. A standalone binary is replaced atomically after its SHA-256 is checked against `checksums.txt`
  and the new binary has run once; a `.deb` or `.rpm` is downloaded, checked and installed through `apt` or
  `dnf` (after a confirmation); an AUR/pacman package is left to its tool. Downloads only from GitHub over
  https, size-capped, and only the `viewdi` file of the archive is read.
- Addresses: a sortable `IP` column in the Table (from 110 columns) and the address at the end of
  each instance row in the Tree. A VM that reports none (no `incus-agent`, like OPNsense) gets it from
  the DHCP leases of its Incus bridge, matched by MAC.
- `b` opens the selected instance in the default browser (the port it publishes, else its address) and
  `B` lets you choose among them. Only private, loopback, link-local and CGNAT IPv4 addresses and
  `localhost` are opened; it works in `--read-only`.

### Fixed
- Create and import forms: the buttons and the key hints are pinned below the fields, so they are
  never cut off; long hints and errors wrap instead of ending in "…"; and a form taller than the
  terminal shows "▲ N more" / "▼ N more" for the fields that are scrolled out of view.
- The "permission denied" hint now looks at the socket instead of always saying "join the group". It
  tells apart a user who is not in the group, a user who is in it but whose login session predates it
  (log out and in, or `sg <group> -c viewdi`), a socket nobody can write to, and a daemon that refused
  although the socket allows access. The old advice (`usermod` then `newgrp`) did not help the second case.

### Added (packaging, from 1.0.0)
- `viewdi --help` with examples, files and environment variables; the man page `viewdi(1)`
  (generated from the flags with `go generate ./cmd/viewdi`, a test fails when it is stale);
  `viewdi completion bash|zsh|fish` and `viewdi man`.
- Release packages built by goreleaser: static `amd64` and `arm64` binaries, `.tar.gz`, `.deb`,
  `.rpm` and checksums, attached to each GitHub release by `release.yml`.
- An Arch Linux package recipe (`packaging/aur/viewdi-bin`).
- `LICENSE` (Apache-2.0), a user guide (`docs/08-user-guide.md`), a bug report template and `SECURITY.md`.

## [0.9.0] - 2026-11-02 · Hardening

### Added
- Crash report: a panic in the UI restores the terminal and writes `crash-<timestamp>.log`
  (version, Go, `$TERM`, stack, types of the last 200 messages; never key text).
- Monochrome theme, used with `NO_COLOR` or `[ui] monochrome = true`.
- Contract suite against real Docker and Incus (`-tags=integration`), a nightly workflow
  (integration, 5 min of fuzzing per target, vulnerability scan, 30 min soak) and `make soak`.
- Nine new fuzz targets and an inventory of every untrusted input.
- Real-daemon chaos runbook and a security review.
- Benchmark gate: a significant slowdown above 15 % fails CI.

### Changed
- Key latency under a log flood is enforced at 50 ms.

### Security
- `xdg-open` only receives `https` links; a warning for `tcp://` Docker hosts without TLS;
  Go toolchain 1.26.10 and `go-jose` 4.1.4.

## [0.8.0] - 2026-11-02 · Acting on updates

### Added
- Pull Docker images (`p`), refresh Incus images (`f`) and recreate Docker containers on the
  new build (`c`) with a preview of what is kept and a rollback on failure.
- Opt-in desktop notifications (D-Bus, then `notify-send`); `--mock-notify`.

### Fixed
- The update checker could drop a check requested right after a report arrived.

## [0.7.0] - 2026-11-02 · Update discovery

### Added
- Updates view (`6`): Docker and Incus releases (package manager or GitHub) and outdated
  container images (registry digests, simplestreams), with a header badge, markers, alerts,
  a toast, snooze and ignore. `[updates] enabled = false` makes no outbound call.

## [0.6.1] - 2026-11-01 · Labs

### Added
- Labs: groups of machines from both engines, linked by hand and drawn as lanes in the Tree
  view with a dot that runs while both ends are up. Saved in `labs.json`.

## [0.6.0] - 2026-11-01 · Tree view

### Added
- Interactive Tree view (`4`) with groupings (network, project, Compose, engine), an inspector
  that follows the cursor, filter, persistence of folds and ASCII icons.

## [0.5.2] - 2026-10-29

### Changed
- Documentation diagrams redrawn and generated from data files (`make docs-diagrams`).

## [0.5.1] - 2026-10-29 · Create and import

### Added
- Create Docker containers and Incus containers and VMs (`n`); import Docker images and rootfs
  tarballs, Incus backups and images (`i`); progress in the Jobs view.

## [0.5.0] - 2026-10-29 · Lifecycle actions

### Added
- Start, stop, restart, pause/resume and delete (`s x R F d`) with confirmation, a job runner,
  the Jobs view (`5`) and `--read-only`.

## [0.4.0] - 2026-10-29 · Detail view

### Added
- Detail view per instance: Overview, live Logs, Inspect, Network, Volumes, Resources.

## [0.3.0] - 2026-10-29 · Metrics

### Added
- Resources and Admin dashboards, CPU and memory columns, alerts, cgroup v2 sampling with an
  API fallback.

## [0.2.0] - 2026-10-29 · Real engines

### Added
- Docker and Incus adapters, supervisor with reconnect, the Table view and auto-discovery of
  the daemon sockets.

## [0.1.0] - 2026-11-02 · Skeleton

### Added
- The shell: six views, layout, key map, nine themes, XDG configuration, `--mock`.

[Unreleased]: https://github.com/blokxz/viewDI/compare/v0.9.0...HEAD
[0.9.0]: https://github.com/blokxz/viewDI/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/blokxz/viewDI/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/blokxz/viewDI/compare/v0.6.1...v0.7.0
[0.6.1]: https://github.com/blokxz/viewDI/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/blokxz/viewDI/compare/v0.5.2...v0.6.0
[0.5.2]: https://github.com/blokxz/viewDI/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/blokxz/viewDI/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/blokxz/viewDI/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/blokxz/viewDI/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/blokxz/viewDI/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/blokxz/viewDI/compare/v0.1.0...v0.2.0
