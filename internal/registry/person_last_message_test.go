package registry

import (
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestThePersonsLastMessageIsTheirLatestUnfailedOneToTheLeadOrItsProject(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, surface.PersonSessionID, "agent")
	recordEndedRow(t, r, surface.Session{ID: "lead"}, `{"id":"lead"}`)
	recordEndedRow(t, r, surface.Session{ID: "worker"}, `{"id":"worker"}`)
	recordEndedRow(t, r, surface.Session{ID: "stranger"}, `{"id":"stranger"}`)
	for id, project := range map[string]string{"worker": `{"id":"repo:x","localId":"p-x"}`, "stranger": `{"id":"repo:y","localId":"p-y"}`} {
		if _, err := r.db.Exec(`UPDATE catalog_sessions SET host_project=? WHERE session_id=?`, []byte(project), id); err != nil {
			t.Fatal(err)
		}
	}
	if last, err := r.PersonLastMessage("lead", "repo:x"); err != nil || last != nil {
		t.Fatalf("with nothing sent: %+v %v", last, err)
	}
	send := func(sender, target, message string) *DeliveryIntent {
		t.Helper()
		intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: sender, TargetSessionID: target, Message: message, Status: DeliveryIntentSent, Evidence: surface.EvidenceTransportAccepted})
		if err != nil {
			t.Fatal(err)
		}
		return intent
	}
	latest := func(want string) {
		t.Helper()
		last, err := r.PersonLastMessage("lead", "repo:x")
		if err != nil || last == nil || last.Message != want || last.At.IsZero() {
			t.Fatalf("last=%+v err=%v, want %q", last, err, want)
		}
	}
	send(surface.PersonSessionID, "lead", "to the lead")
	latest("to the lead")
	send(surface.PersonSessionID, "worker", "to a project session")
	latest("to a project session")
	send(surface.PersonSessionID, "stranger", "to another project")
	send("agent", "lead", "from an agent")
	latest("to a project session")
	failed := send(surface.PersonSessionID, "lead", "never arrived")
	if _, err := failDeliveryIntent(r.db, failed.ID, "gone"); err != nil {
		t.Fatal(err)
	}
	latest("to a project session")
	if last, err := r.PersonLastMessage("", ""); err != nil || last != nil {
		t.Fatalf("no lead and no project match nothing: %+v %v", last, err)
	}
}
