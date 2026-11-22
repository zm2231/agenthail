package cli

import (
	"errors"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestTheOldLeadOfAFolderAddedAsAProjectIsClearedByNameAndItsNameFreed(t *testing.T) {
	f := newGatedFixture(t)
	orphan := registry.CoordinatorSpec{ProjectID: "implicit:" + f.widget, ProjectRoot: f.widget, Name: "vera-lead", Surface: string(surface.KindCodex), Cwd: f.widget}
	if _, err := f.r.SetCoordinator(orphan, registry.CoordinatorChange{SessionID: "widget-other", ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	renamed := registry.CoordinatorSpec{ProjectID: widgetProject, ProjectRoot: f.widget, Name: "vera-lead", Surface: string(surface.KindCodex), Cwd: f.widget}
	var taken registry.CoordinatorNameTakenError
	if _, err := f.r.SetCoordinator(renamed, registry.CoordinatorChange{SessionID: "widget-other", Replace: true, ChangedBy: "fixture"}); !errors.As(err, &taken) {
		t.Fatalf("naming the folder's lead after the old one answered %v, want the name taken", err)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", "")
	if _, err := captureStdout(t, func() error { return f.app.Run([]string{"coordinator", "clear", "--name", "@vera-lead"}) }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.CoordinatorNamed("vera-lead"); !errors.Is(err, registry.ErrNoCoordinator) {
		t.Fatalf("the old lead is still named after the clear: %v", err)
	}
	if _, err := f.r.SetCoordinator(renamed, registry.CoordinatorChange{SessionID: "widget-other", Replace: true, ChangedBy: "fixture"}); err != nil {
		t.Fatalf("the freed name answered %v", err)
	}
}
