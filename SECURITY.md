# Security policy

## Reporting a vulnerability

Please report it privately with a
[GitHub security advisory](https://github.com/blokxz/viewDI/security/advisories/new) on this
repository, not in a public issue. Include the version (`viewdi --version`), what you did and what
happened. You will get an answer as soon as the maintainer can; there is no fixed schedule.

## Supported versions

Only the latest release receives fixes.

## Scope

viewDI runs as your user and talks to the Docker and Incus daemon sockets, which are already
equivalent to root on the host. What the review looked at, and what is accepted, is in
[`docs/07-security-review.md`](docs/07-security-review.md).
