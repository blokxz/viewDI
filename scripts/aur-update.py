#!/usr/bin/env python3
"""Fill packaging/aur/viewdi-bin for a published release.

    scripts/aur-update.py 1.0.0                    # downloads checksums.txt from the release
    scripts/aur-update.py 1.0.0 --checksums FILE   # or reads a local copy

Rewrites pkgver, pkgrel and the sha256sums in PKGBUILD, and regenerates .SRCINFO
(the same file `makepkg --printsrcinfo` would print, so it can be done anywhere).
Afterwards, on Arch: `cd packaging/aur/viewdi-bin && makepkg -si`, then publish.
"""
import argparse, pathlib, re, sys, urllib.request

DIR = pathlib.Path(__file__).resolve().parent.parent / "packaging" / "aur" / "viewdi-bin"
ARCHES = {"x86_64": "amd64", "aarch64": "arm64"}


def checksums(version, local):
    if local:
        text = pathlib.Path(local).read_text()
    else:
        url = f"https://github.com/blokxz/viewDI/releases/download/v{version}/checksums.txt"
        text = urllib.request.urlopen(url, timeout=30).read().decode()
    out = {}
    for line in text.splitlines():
        digest, _, name = line.partition("  ")
        out[name.strip()] = digest.strip()
    return out


def srcinfo(pkgbuild, version, sums):
    def field(name):
        m = re.search(rf"^{name}='([^']*)'", pkgbuild, re.M)
        return m.group(1)
    url = field("url")
    lines = [
        "pkgbase = viewdi-bin",
        f"\tpkgdesc = {field('pkgdesc')}",
        f"\tpkgver = {version}",
        "\tpkgrel = 1",
        f"\turl = {url}",
        "\tarch = x86_64",
        "\tarch = aarch64",
        "\tlicense = Apache-2.0",
        "\toptdepends = docker: manage Docker containers",
        "\toptdepends = incus: manage Incus containers and VMs",
        "\tprovides = viewdi",
        "\tconflicts = viewdi",
        "\toptions = !strip",
        "\tinstall = viewdi.install",
    ]
    for arch, goarch in ARCHES.items():
        lines.append(f"\tsource_{arch} = viewdi-{version}-{arch}.tar.gz::{url}/releases/download/v{version}/viewdi_{version}_linux_{goarch}.tar.gz")
        lines.append(f"\tsha256sums_{arch} = {sums[arch]}")
    lines += ["", "pkgname = viewdi-bin", ""]
    return "\n".join(lines)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("version", help="release version without the v, e.g. 1.0.0")
    ap.add_argument("--checksums", help="local checksums.txt instead of downloading it")
    ap.add_argument("--pkgrel", default="1")
    ap.add_argument("--placeholder", action="store_true", help="write SKIP checksums (the state kept in git)")
    a = ap.parse_args()
    if not re.fullmatch(r"\d+\.\d+\.\d+", a.version):
        sys.exit("version must look like 1.0.0")

    sums = {}
    if a.placeholder:
        sums = {arch: "SKIP" for arch in ARCHES}
    else:
        found = checksums(a.version, a.checksums)
        for arch, goarch in ARCHES.items():
            name = f"viewdi_{a.version}_linux_{goarch}.tar.gz"
            if name not in found:
                sys.exit(f"checksums.txt has no {name}")
            sums[arch] = found[name]

    path = DIR / "PKGBUILD"
    text = path.read_text()
    text = re.sub(r"^pkgver=.*$", f"pkgver={a.version}", text, flags=re.M)
    text = re.sub(r"^pkgrel=.*$", f"pkgrel={a.pkgrel}", text, flags=re.M)
    for arch in ARCHES:
        text = re.sub(rf"^sha256sums_{arch}=\(.*\)$", f"sha256sums_{arch}=('{sums[arch]}')", text, flags=re.M)
    path.write_text(text)
    info = srcinfo(text, a.version, sums).replace("\tpkgrel = 1", f"\tpkgrel = {a.pkgrel}")
    (DIR / ".SRCINFO").write_text(info)
    print(f"viewdi-bin {a.version}-{a.pkgrel}: PKGBUILD and .SRCINFO updated")


if __name__ == "__main__":
    main()
