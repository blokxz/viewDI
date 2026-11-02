# Security Review (Stage 7a)

Scope: what viewDI reads from outside, what it runs, what it writes, and what it sends.
Date: 2026-11-02, against `master` plus Stage 7a. Method: read every `os/exec`, file write,
listener and network client; list what an attacker who controls one input (a registry, a
release feed, a container's logs or name, a file in the config directory) could do.

viewDI runs as the user, never needs root, and warns when it runs as root. It talks to
Docker and Incus, which are themselves root-equivalent: **whoever can use the daemon socket
already owns the host**. The review therefore concerns what viewDI adds on top of that.

## Findings

| # | Area | Finding | Status |
|---|---|---|---|
| 1 | Sockets | A `DOCKER_HOST` / `--docker-host` / `[docker] host` of `tcp://…` without TLS lets anyone on the path control every container. | **Fixed:** a startup warning toast (`docker.PlainRemote`) unless `DOCKER_TLS_VERIFY` is set. viewDI still connects: the choice is the user's. |
| 2 | Subprocess | `xdg-open` received any URL from release feeds and registries (`file:`, `javascript:`, an option-like value, control characters). | **Fixed:** `safeURL` accepts only an absolute `https` URL of at most 2 048 bytes, with no credentials, spaces or control characters; fuzzed. |
| 3 | Untrusted text | The fallback toast of "release notes" printed the URL as received. | **Fixed:** passed through `textsafe.Clean`. |
| 4 | Subprocess | `notify-send` takes summary and body from instance names and release titles. | Already safe: `--` before the text, `textsafe.Clean`, caps (120 / 1 000), markup escaped; same text for D-Bus. |
| 5 | Subprocess | Package-manager queries (`apt`, `pacman`, `dnf`, `zypper`). | Already safe: fixed argument lists, no shell, `LC_ALL=C`, stdin empty, timeout (`pkgmgr.Timeout`), output capped at `MaxOutput`, only read-only subcommands; the package names are constants. |
| 6 | Subprocess | No other `os/exec` use exists (`grep exec.Command`: three call sites, all above). | Checked. |
| 7 | Files | Everything viewDI writes is `0600` in `0700` directories: `state.json`, `labs.json`, `updates.json` (atomic write, temp file in the same directory), the debug log, traces, crash reports. | Checked; crash reports and the trace ring record message **types** only, never key text (a form can hold a secret). |
| 8 | Files | Corrupt `state.json` / `labs.json` are renamed `.bak`; reads are capped (`ReadCapped`). | Checked; both loaders are fuzzed. |
| 9 | Listener | `--pprof` binds only to a loopback address (`checkLoopback`, rejects `0.0.0.0` and hostnames other than `localhost`). It is off by default. | Checked, test exists. |
| 10 | Untrusted text | Log lines, container names, labels, image names and API errors are shown through `textsafe.Clean`/the log sanitiser (escape sequences, controls, bidi overrides stripped; lines capped at 16 KiB). | Checked; fuzzed (`textsafe.FuzzClean`, `docker.FuzzDemux`). |
| 11 | Secrets | Environment values that look secret are masked by default in the Detail view (Overview and Inspect). | Checked. |
| 12 | Network | Outbound calls exist only in the update checker (package manager, GitHub releases API, registry digests) and in image search/pull through the daemon. `[updates] enabled = false` makes no call at all. No telemetry. | Checked. `GITHUB_TOKEN` is read from the environment, sent only to `api.github.com`, never logged. |
| 13 | Destructive actions | Stop, delete, recreate and bulk actions go through the confirm modal; `--read-only` disables every mutation, including import and create. | Checked by existing tests. |
| 14 | Dependencies | The first `govulncheck` run (CI, 2026-11-02) reported 55 findings: the Go standard library of 1.26.0 (fixed up to go1.26.10), `go-jose` v4.1.3, and 24 advisories of `github.com/lxc/incus/v6` v6.23.0 with no fixed version. | **Fixed:** `toolchain go1.26.10` and `go-jose` v4.1.4. **Accepted, with a gate:** the Incus advisories describe bugs of the Incus *server* (project restriction bypasses, nil dereferences restoring backups) filed against the whole module; viewDI links only the client and nothing can be upgraded to. `scripts/vulncheck.py` prints them and still fails on any other called vulnerability, including a new Incus one that has a fix. Revisit when a fixed Incus release appears. |
| 15 | Supply chain | Releases are built from tags by `goreleaser` in CI with checksums (Stage 7b). | Planned in 7b. |

## Added after v1.0.0

| # | Area | Finding | Status |
|---|---|---|---|
| 16 | Self-update | `viewdi --update` downloads and installs code. | Only from `api.github.com`, `github.com` and `*.githubusercontent.com` over https (redirects are checked too), size-capped; the archive is read for one file named exactly `viewdi`, which must be an ELF executable; the SHA-256 is checked against the release's `checksums.txt`; the new binary must run `--version` and report the expected tag before it replaces the old one (atomic rename, same directory, old permissions); packages go through `apt`/`dnf` after an explicit yes, never with a password handled by viewDI. **Accepted:** the checksums come from the same release, so a compromised release is not detected; signatures (cosign) would close it. |
| 17 | Addresses | `b`/`B` send the browser to an address built from engine data. | `safeAddress`: http or https, IPv4 literal in a private, loopback, link-local or CGNAT range, or `localhost`; no credentials, query, fragment, control characters; valid port; fuzzed. Release notes keep the stricter https-only rule. |
| 18 | Engine upgrade | The Updates view `u` runs a privileged command (`sudo apt install --only-upgrade docker-ce`). | The hint text is parsed back only into four fixed templates (apt, dnf, zypper, pacman -Syu) with a strict package-name pattern, because it also travels through `updates.json`; anything else is shown, never run. A confirmation shows the exact command first (and warns that `pacman -Syu` upgrades the whole system). No shell is involved; the terminal is handed over (`tea.ExecProcess`) so `sudo` prompts for its own password and viewDI never sees it. Disabled in `--read-only`; in `--mock` it only says what it would run. Fuzzed. |

## Residual risks (accepted)

- viewDI shows what the daemon reports. A hostile image can name its container anything, set
  any label, and print anything: all of it is sanitised before it reaches the terminal, but a
  convincing fake *within* the allowed characters (a container named like another one) is
  possible. The container ID is always one keystroke away in the Overview.
- The update checker trusts the package manager and the GitHub API over TLS; a compromised
  upstream can report a wrong version, which only produces a badge and a printed upgrade
  hint. viewDI installs nothing on its own: an upgrade runs only after you confirm the exact command (finding 18).
- The Unix socket permission model is the daemon's: the `docker` and `incus-admin` groups
  are root-equivalent. The README (Stage 7b) says so.

## Reporting

Vulnerabilities: use GitHub private security advisories on the repository. The policy file
`SECURITY.md` is added with the Stage 7b documentation.
