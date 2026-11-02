#!/usr/bin/env python3
"""govulncheck gate: fails on any called vulnerability, except advisories of the
Incus module that have no fixed version.

Those advisories describe bugs of the Incus *server* (project restriction
bypasses, nil dereferences while restoring backups) published against the whole
module `github.com/lxc/incus/v6`. viewDI only links its client, nothing can be
upgraded to (the advisories have no fixed version), and a daemon bug is fixed
by upgrading the daemon. They are printed, not ignored silently, and every other
advisory, whatever its module, still fails the build. docs/07-security-review.md
explains the exception.
"""
import json, subprocess, sys

ACCEPTED_MODULE = "github.com/lxc/incus/v6"


def messages(text):
    dec, i = json.JSONDecoder(), 0
    while i < len(text):
        while i < len(text) and text[i].isspace():
            i += 1
        if i >= len(text):
            break
        obj, i = dec.raw_decode(text, i)
        yield obj


def called(findings):
    """OSV id -> (module, fixed version) for findings that reach a function."""
    out = {}
    for f in findings:
        trace = f.get("trace") or []
        if trace and trace[0].get("function"):
            out[f["osv"]] = (trace[0].get("module", ""), f.get("fixed_version", ""))
    return out


def judge(text):
    osv, finds = {}, []
    for m in messages(text):
        if "osv" in m:
            osv[m["osv"]["id"]] = m["osv"].get("summary", "")
        if "finding" in m:
            finds.append(m["finding"])
    bad, accepted = [], []
    for vid, (module, fixed) in sorted(called(finds).items()):
        line = f"{vid} {module} fixed in {fixed or 'N/A'}: {osv.get(vid, '')}"
        (accepted if module == ACCEPTED_MODULE and not fixed else bad).append(line)
    return bad, accepted


def main():
    if len(sys.argv) > 1:  # test mode: read a recorded report
        text = open(sys.argv[1]).read()
    else:
        run = subprocess.run(["govulncheck", "-format", "json", "./..."], capture_output=True, text=True)
        if run.returncode != 0:
            sys.exit(f"govulncheck failed to run:\n{run.stderr}")
        text = run.stdout
    bad, accepted = judge(text)
    for line in accepted:
        print("accepted (Incus server advisory, no fix):", line)
    for line in bad:
        print("VULNERABLE:", line)
    if bad:
        sys.exit(f"{len(bad)} vulnerabilities reach viewDI's code")
    print(f"govulncheck: clean ({len(accepted)} accepted Incus server advisories)")


if __name__ == "__main__":
    main()
