# Changelog

All notable changes to viewDI. The full notes of each release are on the
[releases page](https://github.com/blokxz/viewDI/releases). Versions follow
[Semantic Versioning](https://semver.org/); before `1.0.0` a minor version could change anything.

## [Unreleased]

### Added
- Addresses: a sortable `IP` column in the Table (from 110 columns) and the address at the end of
  each instance row in the Tree. A VM that reports none (no `incus-agent`, like OPNsense) gets it from
  the DHCP leases of its Incus bridge, matched by MAC.
- `b` opens the selected instance in the default browser (the port it publishes, else its address) and
  `B` lets you choose among them. Only private, loopback, link-local and CGNAT IPv4 addresses and
  `localhost` are opened; it works in `--read-only`.

### Added
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
