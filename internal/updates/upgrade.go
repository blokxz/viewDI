package updates

import (
	"fmt"
	"regexp"
	"strings"
)

// The upgrade hints are built by the package-manager sources from fixed
// templates ("sudo apt install --only-upgrade docker-ce"). Running one means
// turning that text back into an argument list, which is only done for those
// templates: the hint also travels through updates.json, so it is treated as
// input, not as a command.
//
//	sudo apt install --only-upgrade <package>
//	sudo dnf upgrade <package>
//	sudo zypper update <package>
//	sudo pacman -Syu
var pkgName = regexp.MustCompile(`^[a-z0-9][a-z0-9+._-]{0,63}$`)

// UpgradeArgs returns the argument list for a hint, or an error when the hint
// is not one of the known templates (a release-page URL from the GitHub source,
// a hand-edited cache, anything else).
func UpgradeArgs(hint string) ([]string, error) {
	f := strings.Fields(hint)
	bad := fmt.Errorf("%q is not an upgrade command viewDI runs", hint)
	if len(f) < 3 || f[0] != "sudo" || strings.ContainsAny(hint, ";|&$`()<>\\\"'\n") {
		return nil, bad
	}
	switch {
	case len(f) == 5 && f[1] == "apt" && f[2] == "install" && f[3] == "--only-upgrade" && pkgName.MatchString(f[4]),
		len(f) == 4 && f[1] == "dnf" && f[2] == "upgrade" && pkgName.MatchString(f[3]),
		len(f) == 4 && f[1] == "zypper" && f[2] == "update" && pkgName.MatchString(f[3]),
		len(f) == 3 && f[1] == "pacman" && f[2] == "-Syu":
		return f, nil
	}
	return nil, bad
}

// FullUpgrade reports whether the command upgrades the whole system rather
// than one package (pacman does not support partial upgrades).
func FullUpgrade(args []string) bool { return len(args) >= 2 && args[1] == "pacman" }
