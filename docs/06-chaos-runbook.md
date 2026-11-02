# Chaos Runbook — real daemons

`make chaos` rotates failure scenarios against the **fake** engines. This runbook is the
other half: the same failures (`05-quality-plan.md` §6, F1–F19) provoked on a **real host**
with real Docker and Incus daemons. It is run by hand, by the maintainer, on their own
machine; it is not a merge gate. Each bug it finds gets a fix and a fake scenario
(`internal/provider/fake/scenario.go`), then a row in the results table below.

## Before you start

- A host with Docker ≥ 29 and Incus ≥ 6, both running, a few instances in each
  (`viewdi-it-*` are fine; see `make test-integration`).
- Run viewDI with the debug log and a trace so a failure can be explained afterwards:
  `bin/viewdi --debug --trace` (log: `~/.local/state/viewDI/viewdi.log`).
- A second terminal for the commands below. Commands marked `sudo` change the host; every
  procedure ends with the undo.
- Pass criteria that apply to **every** row: the UI keeps answering keys, nothing prints on
  the terminal outside the UI, the other engine keeps working, and `ctrl+c` still quits
  within a second.

## Procedures

### F1 — Daemon stopped
1. `sudo systemctl stop docker docker.socket`
2. Expect: Docker row turns degraded, banner with a countdown, Incus unaffected.
3. Press `ctrl+r`: retries at once, still failing, banner stays.
4. `sudo systemctl start docker`: within the backoff the banner clears and the list returns.
5. Repeat with `sudo systemctl stop incus incus.socket` / `start`.
6. Not installed: `bin/viewdi --docker-host unix:///nonexistent.sock`: the "not installed or
   not started" hint with the socket path, no backoff storm.

### F2 — Hung daemon
1. Find the daemon: `pidof dockerd`; `sudo kill -STOP <pid>`.
2. Expect: after at most one request timeout (5 s) the engine is degraded; keys stay responsive;
   `ps -L -p $(pidof viewdi) | wc -l` does not grow while it stays stopped (no goroutine pile-up).
3. `sudo kill -CONT <pid>`: recovers on its own.
4. Same with `incusd`.

### F3 — Event stream stalls
Hard to provoke on a live daemon. Approximation: with Docker degraded and recovered (F2), start
a container from another terminal (`docker run -d --name viewdi-it-ev nginx:alpine`) and check it
shows up within 30 s even if no event arrived. Cleanup: `docker rm -f viewdi-it-ev`.

### F4 — Daemon restarts mid-stream
1. Open the Detail view of a running container, Logs tab, with output flowing
   (`docker run -d --name viewdi-it-log alpine sh -c 'while :; do date; sleep 1; done'`).
2. `sudo systemctl restart docker`.
3. Expect: reconnect banner, then the log resumes with a "reconnected" marker line; the Resources
   graphs resume. The container restarts only if it has a restart policy.
4. Same for an Incus instance and `sudo systemctl restart incus`.
5. Cleanup: `docker rm -f viewdi-it-log`.

### F5 — Permission denied / revoked
1. Docker: `sudo chmod 600 /var/run/docker.sock` (or remove yourself from the group and start a
   new shell). Expect: "Unavailable" with the group hint; a re-probe about every 60 s.
2. Incus: `sudo chmod 600 /var/lib/incus/unix.socket`; same expectation with the `incus-admin` hint.
3. Revoke while connected: apply the `chmod` with viewDI already showing instances.
4. Undo: `sudo chmod 660 /var/run/docker.sock && sudo chgrp docker /var/run/docker.sock`;
   `sudo chmod 660 /var/lib/incus/unix.socket && sudo chgrp incus-admin /var/lib/incus/unix.socket`.

### F6 — Slow daemon / large host
1. Create many instances: `for i in $(seq 200); do docker run -d --name viewdi-it-$i alpine sleep 1d; done`.
2. Expect: the table stays responsive, scrolling does not stutter, no "frozen" spinner.
3. Cleanup: `docker rm -f $(docker ps -aq --filter name=viewdi-it-)`.

### F7 — Malformed API data
Cannot be provoked on a real daemon. Covered by recorded fixtures and fuzz targets. Skip.

### F8 — Log flood
1. `docker run -d --name viewdi-it-flood alpine sh -c 'yes "flood line"'`.
2. Open its Logs tab. Expect: input stays instant, the view refreshes at most about 10 Hz,
   "dropped N lines" shows when filtering lags, memory stays flat (`ps -o rss= -p $(pidof viewdi)`).
3. Cleanup: `docker rm -f viewdi-it-flood`.

### F9 — Hostile log content
1. `docker run -d --name viewdi-it-evil alpine sh -c 'printf "\033]0;pwned\007\033[2J\033[?1049h line\n"; head -c 100000 /dev/zero | tr "\0" a; echo; printf "\377\376 bad utf8\n"; sleep 1d'`
2. Expect: the terminal title does not change, the screen is not cleared, no alternate screen,
   the long line is cut at 16 KiB, the invalid bytes show as replacement characters.
3. Cleanup: `docker rm -f viewdi-it-evil`.

### F10 — Instance removed while viewed
Open the Detail view of a container, then `docker rm -f` it from another terminal. Expect
a "removed" state and disabled actions; no crash, no error loop.

### F11 — Long operation stuck
1. Docker: start a pull of a large image through the create form, then block the registry
   (`sudo iptables -I OUTPUT -d <registry ip> -j DROP`) or unplug the network; cancel the job
   in the Jobs view. Expect: it ends as cancelled/failed promptly.
2. Incus: create an instance from `images:` and cancel it mid-download; the operation is
   cancelled (`incus operation list` is empty afterwards).
3. Undo the firewall rule.

### F12 — Provider panic
Cannot be provoked on a real daemon. Covered by the recover wrapper tests. Skip.

### F13 — UI panic
There is no way to panic a release binary on purpose. The crash log is covered by
`TestReportCrashWritesReadableLog`. Skip.

### F14 — Resize, suspend, hangup
1. Shrink the terminal below 80×24: the "too small" screen; enlarge: the view returns.
2. `ctrl+z`, wait 10 s, `fg`: sampling resumed, graphs show a gap, no spike.
3. Close the terminal window with viewDI running: `pgrep viewdi` is empty a second later.

### F15 — Invalid config, theme, state
1. `echo 'refresh = "garbage' >> ~/.config/viewDI/config.toml`: starts with defaults and a warning toast.
2. Put a broken file in `~/.config/viewDI/themes/bad.toml`: ignored with a warning.
3. `echo '{' > ~/.local/state/viewDI/state.json`: starts clean; the old file is kept as `state.json.bak`.
4. Undo: restore your files.

### F16 — Disk full or read-only state directory
1. `mkdir /tmp/vd-state && sudo mount -t tmpfs -o size=64k tmpfs /tmp/vd-state && dd if=/dev/zero of=/tmp/vd-state/fill bs=1k count=64`
2. `XDG_STATE_HOME=/tmp/vd-state bin/viewdi`; change the theme and quit.
3. Expect: one toast about not saving, no repeated toasts, the UI keeps working.
4. Read-only: `chmod 500 ~/.local/state/viewDI` and repeat.
5. Undo: `sudo umount /tmp/vd-state`; `chmod 700 ~/.local/state/viewDI`.

### F17 — cgroup files unreadable
1. As root, in a container's cgroup directory, make a file unreadable is not possible
   (cgroupfs). Instead run viewDI in a mount namespace without `/sys/fs/cgroup`:
   `sudo unshare -m sh -c 'umount -l /sys/fs/cgroup; exec runuser -u $USER -- bin/viewdi'`.
2. Expect: metrics fall back to the engine API; the sampling health indicator shows the fallback.

### F18 — Update checker offline or rate limited
1. Disconnect the network (or `sudo iptables -I OUTPUT -p tcp --dport 443 -j REJECT`), press the
   update-check key in the Updates view.
2. Expect: "last check failed" with the next retry time, no toast on every retry.
3. With `[updates] enabled = false`, `ss -tnp | grep viewdi` shows no outbound connections.
4. Undo the firewall rule.

### F19 — Registry hangs
1. `sudo iptables -I OUTPUT -p tcp --dport 443 -j DROP` (packets vanish, nothing is refused).
2. Run an image check. Expect: the UI never waits more than the 30 s per-check timeout, up to 4
   checks at a time, the container list is unaffected, the last good image results stay.
3. Undo the firewall rule.

## Results

Fill in one row per run; link the fix or the issue for anything that did not pass.

| Date | viewDI | Docker | Incus | Rows run | Failed rows | Fix / issue |
|---|---|---|---|---|---|---|
| _not run yet_ | | | | | | |
