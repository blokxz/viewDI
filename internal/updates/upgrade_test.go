package updates

import (
	"reflect"
	"testing"
)

func TestUpgradeArgs(t *testing.T) {
	ok := map[string][]string{
		"sudo apt install --only-upgrade docker-ce": {"sudo", "apt", "install", "--only-upgrade", "docker-ce"},
		"sudo apt install --only-upgrade incus":     {"sudo", "apt", "install", "--only-upgrade", "incus"},
		"sudo dnf upgrade moby-engine":              {"sudo", "dnf", "upgrade", "moby-engine"},
		"sudo zypper update incus":                  {"sudo", "zypper", "update", "incus"},
		"sudo pacman -Syu":                          {"sudo", "pacman", "-Syu"},
		"  sudo   dnf   upgrade   incus ":           {"sudo", "dnf", "upgrade", "incus"},
	}
	for hint, want := range ok {
		got, err := UpgradeArgs(hint)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %v, %v", hint, got, err)
		}
	}
	bad := []string{
		"", "sudo", "apt install --only-upgrade docker-ce", // no sudo
		"sudo apt install --only-upgrade docker-ce; rm -rf /", "sudo apt install --only-upgrade $(id)",
		"sudo apt install --only-upgrade docker-ce && reboot", "sudo apt install --only-upgrade `id`",
		"sudo apt install docker-ce", "sudo apt install --only-upgrade -o APT::x=y docker-ce",
		"sudo apt install --only-upgrade ../etc", "sudo apt install --only-upgrade -y",
		"sudo dnf upgrade --refresh incus", "sudo pacman -S evil", "sudo pacman -Syu extra",
		"sudo sh -c 'x'", "https://github.com/lxc/incus/releases", "sudo dnf upgrade Incus", "sudo dnf upgrade " + string(make([]byte, 70)),
		"sudo apt install --only-upgrade docker-ce\nsudo reboot",
	}
	for _, hint := range bad {
		if got, err := UpgradeArgs(hint); err == nil {
			t.Errorf("%q accepted as %v", hint, got)
		}
	}
	if args, _ := UpgradeArgs("sudo pacman -Syu"); !FullUpgrade(args) {
		t.Error("pacman -Syu upgrades the whole system")
	}
	if args, _ := UpgradeArgs("sudo dnf upgrade incus"); FullUpgrade(args) {
		t.Error("dnf upgrade <pkg> is a single package")
	}
}

func FuzzUpgradeArgs(f *testing.F) {
	for _, s := range []string{"sudo apt install --only-upgrade docker-ce", "sudo pacman -Syu", "", "sudo \x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		args, err := UpgradeArgs(s)
		if err != nil {
			return
		}
		if args[0] != "sudo" {
			t.Fatalf("accepted %q as %v", s, args)
		}
		for _, a := range args {
			if a == "" || a[0] == ';' || a[0] == '$' {
				t.Fatalf("suspicious argument in %v", args)
			}
		}
	})
}
