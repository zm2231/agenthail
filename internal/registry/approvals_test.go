package registry

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func raiseTestApproval(t *testing.T, r *Registry, session, payload string, at time.Time) (Ask, bool) {
	t.Helper()
	ask, raised, err := r.RaiseApproval(Ask{Title: "@worker asks to rename a project", Questions: []AskQuestion{{Question: "Rename project p to q", Options: []AskOption{{ID: ApprovalAllow, Label: "Allow once"}, {ID: ApprovalDeny, Label: "Deny"}}}}},
		Approval{SessionID: session, API: "action", Payload: json.RawMessage(payload), Description: "Rename project p to q", ExpiresAt: at.Add(24 * time.Hour)}, at)
	if err != nil {
		t.Fatal(err)
	}
	return ask, raised
}

func TestAnApprovalAskShowsOnItsRowAndAmongTheOpenAsks(t *testing.T) {
	r := coordinatorRegistry(t, "worker")
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	ask, _ := raiseTestApproval(t, r, "worker", `{"action":"project-rename"}`, at)

	rows, err := r.RowAsks()
	if err != nil {
		t.Fatal(err)
	}
	if row := rows["worker"]; row.ID != ask.ID || !slices.Equal(row.Open, []string{ask.ID}) || row.RoutedTo != AskToPerson || row.Deadline.IsZero() {
		t.Fatalf("the row shows %s open %v routed %q deadline %v, want the approval %s open for the person with its deadline", row.ID, row.Open, row.RoutedTo, row.Deadline, ask.ID)
	}
}

func TestAnApprovalIsBoundToItsPayloadAndDecidedOnce(t *testing.T) {
	r := coordinatorRegistry(t, "worker")
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	ask, raised := raiseTestApproval(t, r, "worker", `{"action":"project-rename","title":"q"}`, at)
	again, raisedAgain := raiseTestApproval(t, r, "worker", `{"action":"project-rename","title":"q"}`, at.Add(time.Minute))
	if !raised || raisedAgain || again.ID != ask.ID {
		t.Fatalf("the same payload while open raised %v (%s), want the open ask %s back", raisedAgain, again.ID, ask.ID)
	}
	if other, raisedOther := raiseTestApproval(t, r, "worker", `{"action":"project-rename","title":"z"}`, at); !raisedOther || other.ID == ask.ID {
		t.Fatal("a different payload must be its own approval")
	}

	if decided, err := r.DecideApproval(ask.ID, ApprovalAllow, "Rename project p to q", "phone", "device", at.Add(time.Hour)); err != nil || !decided {
		t.Fatalf("allow: %v %v", decided, err)
	}
	if decided, err := r.DecideApproval(ask.ID, ApprovalAllow, "Rename project p to q", "phone", "device", at.Add(time.Hour)); err != nil || decided {
		t.Fatalf("a second answer decided %v (%v), want it refused", decided, err)
	}
	unran, err := r.UnranApprovals()
	if err != nil || len(unran) != 1 || unran[0].AskID != ask.ID || string(unran[0].Payload) != `{"action":"project-rename","title":"q"}` {
		t.Fatalf("allowed and not yet run: %+v %v", unran, err)
	}
	if err := r.RecordApprovalRun(ask.ID, ApprovalRan, "ok", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	stored, _, err := r.Approval(ask.ID)
	if err != nil || stored.Status != ApprovalRan || stored.DecidedBy != "phone" || stored.RanAt.IsZero() || stored.PayloadHash != ApprovalPayloadHash("action", []byte(`{"action":"project-rename","title":"q"}`)) {
		t.Fatalf("the record is %+v (%v)", stored, err)
	}
	if unran, _ := r.UnranApprovals(); len(unran) != 0 {
		t.Fatalf("a run approval is still listed as unrun: %+v", unran)
	}
}

func TestAnUnansweredApprovalExpiresAtItsDeadline(t *testing.T) {
	r := coordinatorRegistry(t, "worker")
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	ask, _ := raiseTestApproval(t, r, "worker", `{"action":"project-archive"}`, at)
	if due, err := r.ExpireApprovals(at.Add(23 * time.Hour)); err != nil || len(due) != 0 {
		t.Fatalf("before its deadline %d expired (%v)", len(due), err)
	}
	due, err := r.ExpireApprovals(at.Add(24 * time.Hour))
	if err != nil || len(due) != 1 || due[0].ID != ask.ID {
		t.Fatalf("at its deadline expired %+v (%v)", due, err)
	}
	if decided, _ := r.DecideApproval(ask.ID, ApprovalAllow, "q", "phone", "device", at.Add(25*time.Hour)); decided {
		t.Fatal("an expired approval was allowed")
	}
	stored, _, _ := r.Approval(ask.ID)
	if stored.Status != ApprovalExpired {
		t.Fatalf("status %q, want expired", stored.Status)
	}
}

func TestAnApprovalAnsweredAfterItsDeadlineIsNotDecidedEvenBeforeTheSweep(t *testing.T) {
	r := coordinatorRegistry(t, "worker")
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	ask, _ := raiseTestApproval(t, r, "worker", `{"action":"project-archive"}`, at)
	if decided, err := r.DecideApproval(ask.ID, ApprovalAllow, "q", "phone", "device", at.Add(24*time.Hour)); err != nil || decided {
		t.Fatalf("an approval answered at its deadline was decided: %v %v", decided, err)
	}
	if decided, err := r.DecideApproval(ask.ID, ApprovalAllow, "q", "phone", "device", at.Add(24*time.Hour-time.Millisecond)); err != nil || !decided {
		t.Fatalf("an approval answered just before its deadline was not decided: %v %v", decided, err)
	}
}

func TestASessionIsLabelledAsItsRowIsWhetherLocalOrOnAPairedHost(t *testing.T) {
	r := coordinatorRegistry(t, "worker")
	hostID := "h3a1b97d15648"
	if _, err := r.AddHost(PairedHost{ID: hostID, Name: "mac", Endpoint: "https://mac.example.ts.net:7412", Address: "100.64.0.3", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	entity := hostID + "::cd55560f-9682-4c82-b2da-7731e3d47dbb"
	if _, _, err := r.ApplyHostCatalogRow(hostID, HostCatalogRow{Session: surface.Session{ID: "cd55560f-9682-4c82-b2da-7731e3d47dbb", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "agenthail-collab"}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `","alias":"collab"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("builder", "worker"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string][2]string{entity: {"collab", "agenthail-collab"}, "worker": {"builder", ""}} {
		alias, name, err := r.SessionLabel(id)
		if err != nil || alias != want[0] || (want[1] != "" && name != want[1]) {
			t.Errorf("%s is labelled alias=%q name=%q (%v), want %v", id, alias, name, err, want)
		}
	}
}
