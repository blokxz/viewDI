# viewdi-bin (AUR)

Binary package built from the release tarballs (`x86_64` and `aarch64`). Published by hand.

After a release exists on GitHub:

```sh
scripts/aur-update.py 1.0.0        # fills the checksums from the release's checksums.txt
cd packaging/aur/viewdi-bin
makepkg -si                        # on Arch: build and install to test it
git clone ssh://aur@aur.archlinux.org/viewdi-bin.git /tmp/aur-viewdi-bin
cp PKGBUILD .SRCINFO /tmp/aur-viewdi-bin/ && cd /tmp/aur-viewdi-bin
git add PKGBUILD .SRCINFO && git commit -m "viewdi-bin 1.0.0" && git push
```

The checked-in `PKGBUILD` has `SKIP` checksums until the script fills them in.
