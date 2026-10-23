package registrytest

import (
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestEachCopyIsItsOwnRegistryWithItsOwnHostIDAndEpoch(t *testing.T) {
	first, err := registry.Open(Path(t))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := registry.Open(Path(t))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	firstEpoch, _, err := first.CatalogState()
	if err != nil {
		t.Fatal(err)
	}
	secondEpoch, _, err := second.CatalogState()
	if err != nil {
		t.Fatal(err)
	}
	if firstEpoch == "" || firstEpoch == secondEpoch {
		t.Fatalf("host epochs %q and %q, want two different ones", firstEpoch, secondEpoch)
	}
	firstHost, err := first.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	secondHost, err := second.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if firstHost.ID == "" || firstHost.ID == secondHost.ID {
		t.Fatalf("host ids %q and %q, want two different ones", firstHost.ID, secondHost.ID)
	}
	if err := first.RegisterSession(surface.Session{ID: "only-here", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Session("only-here"); err == nil {
		t.Fatal("a session registered in one copy is in another")
	}
}
