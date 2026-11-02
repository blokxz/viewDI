# viewDI

A terminal UI for Linux that manages and monitors **Docker** and **Incus** side by side:
one table, one tree, one set of keys, whichever engine a container or VM lives in.

![viewDI](docs/demos/stage-6b.gif)

- **Lifecycle:** create, import, start, stop, restart, pause, delete and recreate Docker
  containers and Incus containers and VMs, with progress and a confirmation before anything destructive.
- **Table and Tree views:** sort and filter everything, or walk engine → network or project →
  instance → ports and mounts with an inspector that follows the cursor. Group machines of
  both engines into *labs* and link them.
- **Detail view:** overview, live logs (pause, filter, wrap), inspect, network, volumes, resources.
- **Metrics and alerts:** CPU, memory, network and disk per engine and per instance, with alerts
  for memory, CPU, restarts and failing health checks.
- **Updates:** new Docker and Incus releases and outdated container images, as a badge, alerts,
  a toast and an Updates view, with opt-in desktop notifications. viewDI never installs anything
  by itself; it shows the command.
- **Resilient:** a dead or hung daemon never freezes the UI; a banner counts down to the next
  reconnect and the other engine keeps working.
- **Themes:** Everforest, Dracula, Catppuccin, Monokai, Gruvbox, Nord, Tokyo Night, your own,
  and a colourless one for `NO_COLOR`.

## Install

Packages for every release are attached to the
[GitHub release](https://github.com/blokxz/viewDI/releases): static binaries (`amd64` and
`arm64`), `.deb`, `.rpm` and `checksums.txt`. Check the download against the checksums first:

```sh
sha256sum --check --ignore-missing checksums.txt
```

| System | Command |
|---|---|
| Debian, Ubuntu | `sudo apt install ./viewdi_<version>_linux_amd64.deb` |
| Fedora, openSUSE | `sudo dnf install ./viewdi_<version>_linux_amd64.rpm` |
| Arch Linux | build `packaging/aur/viewdi-bin` with `makepkg -si` (an AUR upload is planned) |
| Anywhere | unpack the `.tar.gz` and put `viewdi` in your `PATH`; the man page and the completions are in the archive |
| From source | Go ≥ 1.26: `make build` → `bin/viewdi` |

### Updating

```sh
viewdi --update
```

finds the latest GitHub release and shows what it will do before doing it (`--yes` skips the question):

- **Tarball or any copy you own:** downloads the matching archive, checks its SHA-256 against the release's
  `checksums.txt`, runs the new binary once, and replaces the old one atomically (the old one is kept if
  anything fails). If the directory is not yours, run `sudo viewdi --update`.
- **`.deb` or `.rpm`:** downloads and checks the package, shows the command (`sudo apt install /tmp/…deb`) and
  runs it only if you say yes; `sudo` asks for its password on your terminal.
- **AUR/pacman:** prints the command for your AUR helper.
- A development build (`dev`, a snapshot) has no version to compare; `--force` installs the latest release over it.

The checksums are published with the release itself, so they protect against a corrupt download, not against a
compromised release (signatures are not published yet). Inside viewDI, the Updates view (`6`) reports; `u` on an engine row shows the exact upgrade command (for example `sudo apt install --only-upgrade docker-ce`) and, if you confirm, runs it in your terminal where `sudo` asks for its password.

The packages install the man page (`man viewdi`) and the bash, zsh and fish completions.
`viewdi completion bash|zsh|fish` prints them if you installed the tarball.

## Permissions

viewDI never needs root; it talks to the daemon sockets, so your user needs the same access as
the `docker` and `incus` command line tools:

- **Docker:** be in the `docker` group (`sudo usermod -aG docker $USER`, then log in again) or
  use rootless Docker. Both are discovered automatically; `DOCKER_HOST` and `--docker-host` override.
- **Incus:** be in `incus-admin` (full access) or `incus` (restricted project).
  `INCUS_SOCKET` and `--incus-socket` override.

Both groups are equivalent to root on the host. If access is missing, viewDI says which group to join.
`--read-only` disables every action, for a monitoring-only session.

## Quick start

```sh
viewdi                # real daemons
viewdi --mock         # try it first on made-up containers, no daemon needed
viewdi --help
```

`1`–`6` switch views (Admin, Resources, Table, Tree, Jobs, Updates), `tab` cycles panes,
`enter` opens an instance, `esc` goes back, `n` creates, `i` imports, `T` picks a theme, `?` lists
every binding and `q` quits. The per-view keys are in the [user guide](docs/08-user-guide.md).

## Configuration

Settings live in `~/.config/viewDI/config.toml` (XDG paths are honoured). viewDI never rewrites
it, and an invalid file never stops the app: defaults are used and a toast says why. The sections
are `[ui]`, `[tree]`, `[docker]`, `[incus]`, `[alerts]`, `[logs]`, `[actions]` and `[updates]`; the
[user guide](docs/08-user-guide.md#configuration-reference) lists every key with its default.

| File | Purpose |
|---|---|
| `~/.config/viewDI/config.toml` | settings |
| `~/.config/viewDI/themes/*.toml` | your themes |
| `~/.config/viewDI/keys.toml` | key overrides |
| `~/.local/state/viewDI/state.json` | last view, theme, sort, folded nodes |
| `~/.local/state/viewDI/crash-*.log` | crash reports |
| `~/.local/share/viewDI/labs.json` | your labs |
| `~/.cache/viewDI/updates.json` | update check cache |

## Privacy and safety

- The only outbound network calls are the update checks (package manager queries, GitHub releases,
  registry digests) and whatever the daemons do for pulls. `[updates] enabled = false` turns every
  check off. There is no telemetry.
- Secret-looking environment values are masked by default.
- Text from containers (logs, names, labels) is stripped of terminal escape sequences before display.
- Links are opened in your browser only when they are `https`.
- More in [`docs/07-security-review.md`](docs/07-security-review.md). To report a vulnerability, see
  [`SECURITY.md`](SECURITY.md).

## Troubleshooting

| Symptom | What to do |
|---|---|
| "permission denied" on a socket | join the group shown in the message, then log in again |
| A daemon is "not installed or not started" | start it (`systemctl start docker`, `systemctl start incus`); viewDI reconnects by itself, or press `ctrl+r` |
| Odd glyphs | use a Nerd Font, or set `[ui] ascii_icons = true` (automatic on `TERM=linux`) |
| Colours look wrong or you want none | `NO_COLOR=1 viewdi` or `[ui] monochrome = true` |
| A crash | the path of `crash-*.log` is printed; attach it to a bug report |

Bug reports: use the issue template; it asks for the version, engines, socket type, terminal and `$TERM`.
`viewdi --debug` writes `~/.local/state/viewDI/viewdi.log`, and `--trace` records the UI messages for a replay.

## Development

```sh
make test               # race detector, shuffled, goroutine leak checks
make lint               # golangci-lint v2
make golden-update      # regenerate golden frames after an intended UI change
make fuzz               # every fuzz target for FUZZTIME (default 30s)
make bench              # benchmarks, compared with master in CI
make soak               # memory and goroutines over time on the chaos mock
make test-integration   # the contract suite against real Docker and Incus
go generate ./cmd/viewdi  # regenerate docs/man/viewdi.1 after changing flags
```

Design documents live in [`docs/`](docs/); `CLAUDE.md` summarises the conventions.
Release packages are built by goreleaser (`goreleaser release --snapshot --clean` to try it).

## License

Apache License 2.0. See [`LICENSE`](LICENSE).
