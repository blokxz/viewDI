#!/bin/sh
# Runs after the .deb or .rpm is installed (as root). It only prints: it never
# fails the installation and never changes the system. viewDI itself needs
# neither Docker nor Incus to install, but needs at least one to be useful.
note() { printf 'viewdi: %s\n' "$1"; }

have_docker=no
have_incus=no
command -v docker >/dev/null 2>&1 && have_docker=yes
command -v incus >/dev/null 2>&1 && have_incus=yes

if [ "$have_docker" = no ] && [ "$have_incus" = no ]; then
	note "neither docker nor incus was found. Install at least one engine to have something to manage."
else
	[ "$have_docker" = no ] && note "docker not found (optional): only Incus will be managed."
	[ "$have_incus" = no ] && note "incus not found (optional): only Docker will be managed."
fi
note "to use the sockets your user must be in the docker and/or incus-admin group"
note "(then log out and back in). As your regular user, run: viewdi doctor"
exit 0
