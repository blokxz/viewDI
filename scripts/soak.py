#!/usr/bin/env python3
"""Soak test: runs viewDI against the chaos mock for a long time, drives the UI,
and fails when memory or goroutines keep growing after the warm-up.

    scripts/soak.py [--minutes 30] [--binary bin/viewdi] [--scenario chaos]
                    [--out soak.csv] [--heap-profile soak-heap.pb.gz]

Every 30 s it records RSS (/proc), the live heap after a GC and the goroutine
count (--pprof endpoint on loopback). The first 20 % of the run is warm-up. It
compares the mean of the first and the last quarter of the rest and fails when
the heap grew by more than 20 % AND 8 MiB, or the goroutines by more than 25 %
AND 20. On failure it saves a heap profile for `go tool pprof`.
"""
import argparse, csv, fcntl, os, pty, re, select, shutil, signal, socket, struct
import sys, tempfile, termios, time, urllib.request

KEYS = [b"1", b"2", b"3", b"4", b"5", b"6", b"3", b"\r", b"\t", b"\t", b"\x1b", b"4", b"j", b"j", b"k", b"/", b"w", b"\x1b", b"?", b"\x1b"]


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def get(url, timeout=30):
    return urllib.request.urlopen(url, timeout=timeout).read()


def heap_inuse(base):
    text = get(base + "/debug/pprof/heap?debug=1&gc=1").decode("latin-1")
    return int(re.search(r"# HeapInuse = (\d+)", text).group(1))


def goroutines(base):
    text = get(base + "/debug/pprof/goroutine?debug=1").decode("latin-1")
    return int(re.match(r"goroutine profile: total (\d+)", text).group(1))


def rss_mib(pid):
    for line in open(f"/proc/{pid}/status"):
        if line.startswith("VmRSS"):
            return int(line.split()[1]) / 1024


def mean(xs):
    return sum(xs) / len(xs)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--minutes", type=float, default=30)
    ap.add_argument("--binary", default="bin/viewdi")
    ap.add_argument("--scenario", default="chaos")
    ap.add_argument("--out", default="soak.csv")
    ap.add_argument("--heap-profile", default="soak-heap.pb.gz")
    ap.add_argument("--interval", type=float, default=30)
    a = ap.parse_args()

    home = tempfile.mkdtemp(prefix="viewdi-soak-")
    port = free_port()
    base = f"http://127.0.0.1:{port}"
    env = dict(os.environ, TERM="xterm-256color", HOME=home, XDG_CONFIG_HOME=home + "/c",
               XDG_STATE_HOME=home + "/s", XDG_CACHE_HOME=home + "/k", XDG_DATA_HOME=home + "/d")
    pid, fd = pty.fork()
    if pid == 0:
        os.execve(a.binary, [a.binary, f"--mock={a.scenario}", f"--pprof=127.0.0.1:{port}"], env)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))

    rows, t0, key_i, last_sample, last_key = [], time.time(), 0, 0.0, 0.0
    total = a.minutes * 60
    try:
        while time.time() - t0 < total:
            r, _, _ = select.select([fd], [], [], 0.2)
            if r:
                try:
                    os.read(fd, 65536)  # the terminal must be drained
                except OSError:
                    sys.exit("viewdi exited during the soak")
            now = time.time()
            if now - last_key > 2:
                os.write(fd, KEYS[key_i % len(KEYS)])
                key_i, last_key = key_i + 1, now
            if now - last_sample >= a.interval:
                last_sample = now
                row = {"t_s": round(now - t0), "rss_mib": round(rss_mib(pid), 1),
                       "heap_mib": round(heap_inuse(base) / 2**20, 2), "goroutines": goroutines(base)}
                rows.append(row)
                print(row, flush=True)
        verdict = judge(rows)
        if verdict:
            try:
                open(a.heap_profile, "wb").write(get(base + "/debug/pprof/heap?gc=1"))
            except Exception as e:  # the profile is a courtesy
                print("could not save the heap profile:", e)
    finally:
        try:
            os.kill(pid, signal.SIGTERM)
            os.waitpid(pid, 0)
        except OSError:
            pass
        shutil.rmtree(home, ignore_errors=True)
        if rows:
            with open(a.out, "w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=list(rows[0]))
                w.writeheader()
                w.writerows(rows)
    if verdict:
        sys.exit("SOAK FAILED: " + verdict)
    print("soak ok")


def judge(rows):
    if len(rows) < 12:
        return f"only {len(rows)} samples; run at least 6 minutes"
    rows = rows[len(rows) // 5:]  # drop the warm-up
    q = max(2, len(rows) // 4)
    problems = []
    for key, rel, absolute, unit in (("heap_mib", 0.20, 8, "MiB"), ("goroutines", 0.25, 20, "")):
        first, last = mean([r[key] for r in rows[:q]]), mean([r[key] for r in rows[-q:]])
        grew = last - first
        print(f"{key}: first quarter {first:.1f}, last quarter {last:.1f} ({grew:+.1f}{unit})")
        if grew > absolute and grew > first * rel:
            problems.append(f"{key} grew from {first:.1f} to {last:.1f}{unit}")
    return "; ".join(problems)


if __name__ == "__main__":
    main()
