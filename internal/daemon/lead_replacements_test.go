package daemon

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func (f *coordinatorFixture) projectLead(t *testing.T) dashboardProjectCoordinator {
	t.Helper()
	state, err := f.daemon.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range state.Projects {
		if project.ID == "project-fixture" && len(project.Coordinators) == 1 {
			return project.Coordinators[0]
		}
	}
	t.Fatalf("projects=%+v, want project-fixture with its lead", state.Projects)
	return dashboardProjectCoordinator{}
}

func TestTheSnapshotShowsAReplacedLeadUntilThePersonAcknowledgesIt(t *testing.T) {
	f := newCoordinatorFixture(t)
	if code, body := f.send(t, "replace", goneCoordinator, "plan"); code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, body)
	}
	entry := f.handover(t)
	state, err := f.daemon.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.LeadReplacements) != 1 {
		t.Fatalf("lead replacements=%+v", state.LeadReplacements)
	}
	shown := state.LeadReplacements[0]
	if shown.ID != entry.ID || shown.Name != "fixture-lead" || shown.SessionID != entry.SessionID || shown.PreviousSessionID != goneCoordinator || shown.ChangedBy != "the person" || shown.Reason == "" {
		t.Fatalf("lead replacement=%+v", shown)
	}
	if lead := f.projectLead(t); lead.Replaced == nil || lead.Replaced.ID != entry.ID {
		t.Fatalf("project lead=%+v, want its last replacement", lead)
	}
	if base := newSnapshotBase(state); len(base.LeadReplacements) != 1 || base.LeadReplacements[0].ID != entry.ID {
		t.Fatalf("the base snapshot leaves out the open replacement: %+v", base.LeadReplacements)
	}
	ack := fmt.Sprintf(`{"action":"lead-replacement-ack","replacementId":%d}`, entry.ID)
	if response := serveV1Action(f.handler, ack, "ack-1", f.token); response.Code != http.StatusOK {
		t.Fatalf("ack status=%d body=%s", response.Code, response.Body)
	}
	if response := serveV1Action(f.handler, ack, "ack-2", f.token); response.Code != http.StatusNotFound {
		t.Fatalf("second ack status=%d body=%s", response.Code, response.Body)
	}
	if state, err = f.daemon.dashboardState(context.Background()); err != nil || len(state.LeadReplacements) != 0 {
		t.Fatalf("lead replacements after ack=%+v err=%v", state.LeadReplacements, err)
	}
	if lead := f.projectLead(t); lead.Replaced == nil || lead.Replaced.ID != entry.ID {
		t.Fatalf("project lead after ack=%+v, want its history kept", lead)
	}
}
