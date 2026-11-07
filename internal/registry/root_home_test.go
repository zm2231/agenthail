package registry

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func ownersOf(owners map[string]uint32) func(string) (uint32, error) {
	return func(path string) (uint32, error) {
		if owner, ok := owners[path]; ok {
			return owner, nil
		}
		return 0, fs.ErrNotExist
	}
}

func TestRootIsRefusedInAPersonsHome(t *testing.T) {
	const person = uint32(4242)
	const registryPath = "/srv/person/.agenthail/registry.db"
	for name, owners := range map[string]map[string]uint32{
		"no state yet":                  {"/": 0, "/srv": 0, "/srv/person": person},
		"state left root-owned earlier": {"/": 0, "/srv": 0, "/srv/person": person, "/srv/person/.agenthail": 0},
		"state belonging to the person": {"/": 0, "/srv": 0, "/srv/person": person, "/srv/person/.agenthail": person},
	} {
		t.Run(name, func(t *testing.T) {
			err := refuseRootInAnothersHome(registryPath, 0, ownersOf(owners))
			if err == nil || !strings.Contains(err.Error(), "don't run agenthail with sudo; run it as") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestRootIsRefusedForARegistryDirectlyInAPersonsHome(t *testing.T) {
	err := refuseRootInAnothersHome("/srv/person/custom.db", 0, ownersOf(map[string]uint32{"/": 0, "/srv": 0, "/srv/person": 4242}))
	if err == nil || !strings.Contains(err.Error(), "don't run agenthail with sudo") {
		t.Fatalf("err=%v", err)
	}
}

func TestRootIsAllowedInItsOwnHome(t *testing.T) {
	for name, registryPath := range map[string]string{
		"its home":         "/root/.agenthail/registry.db",
		"a query string":   "/root/.agenthail/registry.db?cache=shared",
		"a home not there": "/root/missing/.agenthail/registry.db",
	} {
		t.Run(name, func(t *testing.T) {
			if err := refuseRootInAnothersHome(registryPath, 0, ownersOf(map[string]uint32{"/": 0, "/root": 0})); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRootWithNoAbsoluteHomeCarriesOnWhoeverOwnsTheWorkingDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("this runner's temp directory belongs to root")
	}
	effectiveUID = func() int { return 0 }
	t.Cleanup(func() { effectiveUID = os.Geteuid })
	t.Chdir(t.TempDir())
	for name, home := range map[string]string{"empty": "", "relative": "some/home"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", home)
			if err := RefuseSudoHome(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAnOrdinaryUserIsNotAskedWhoOwnsTheHome(t *testing.T) {
	err := refuseRootInAnothersHome("/srv/person/.agenthail/registry.db", 501, func(string) (uint32, error) {
		t.Fatal("an ordinary user's open read an owner")
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTheRefusalNamesTheAccountThatOwnsTheHome(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	home := t.TempDir()
	refusal := refuseRootInAnothersHome(filepath.Join(home, ".agenthail", "registry.db"), 0, ownerOfPath)
	if os.Geteuid() == 0 {
		t.Skip("this runner owns its temp directory as root")
	}
	if refusal == nil || !strings.HasSuffix(refusal.Error(), "run it as "+me.Username) {
		t.Fatalf("refusal=%v, want %s", refusal, me.Username)
	}
}

func TestEveryOpenAndARestoreRefuseBeforeCreatingAnything(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("this runner's temp directory belongs to root")
	}
	effectiveUID = func() int { return 0 }
	t.Cleanup(func() { effectiveUID = os.Geteuid })
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".agenthail", "registry.db")
	for name, attempt := range map[string]func() error{
		"Open":        func() error { _, err := Open(path); return err },
		"OpenDefault": func() error { _, err := OpenDefault(context.Background(), true); return err },
		"OpenCurrent": func() error { _, err := OpenCurrent(path); return err },
		"restore":     func() error { _, err := restoreMigrationBackup(path, 1); return err },
		"any command": RefuseSudoHome,
	} {
		err := attempt()
		if err == nil || !strings.Contains(err.Error(), "don't run agenthail with sudo") {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("created %v, err=%v", entries, err)
	}
}

func TestAnUnreadableOwnerRefuses(t *testing.T) {
	denied := errors.New("denied")
	err := refuseRootInAnothersHome("/srv/person/.agenthail/registry.db", 0, func(string) (uint32, error) { return 0, denied })
	if !errors.Is(err, denied) {
		t.Fatalf("err=%v", err)
	}
}
