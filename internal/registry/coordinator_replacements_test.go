package registry

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAHandoverRecordsWhoAndWhyAndStaysOpenUntilAnnouncedAndAcknowledged(t *testing.T) {
	r := coordinatorRegistry(t, "first", "second", "worker")
	spec := CoordinatorSpec{ProjectID: "project-work", ProjectRoot: "/work", Name: "work-lead", Surface: "claude", Cwd: "/work"}
	if _, err := r.SetCoordinator(spec, CoordinatorChange{SessionID: "first", ChangedBy: "the CLI"}); err != nil {
		t.Fatal(err)
	}
	id, err := r.HandOverCoordinator(spec, "first", "second", CoordinatorHandover{ChangedBy: "worker", Reason: "gone: conversation first no longer exists", NotifySender: true})
	if err != nil || id == 0 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	unannounced, err := r.UnannouncedCoordinatorReplacements()
	if err != nil || len(unannounced) != 1 || unannounced[0].ID != id || unannounced[0].ChangedBy != "worker" || unannounced[0].Reason != "gone: conversation first no longer exists" || !unannounced[0].NotifySender || unannounced[0].PreviousSessionID != "first" || unannounced[0].SessionID != "second" {
		t.Fatalf("unannounced=%+v err=%v", unannounced, err)
	}
	now := time.Now()
	if err := r.MarkCoordinatorReplacementAnnounced(id, now); err != nil {
		t.Fatal(err)
	}
	if unannounced, err := r.UnannouncedCoordinatorReplacements(); err != nil || len(unannounced) != 0 {
		t.Fatalf("still unannounced=%+v err=%v", unannounced, err)
	}
	if open, err := r.OpenCoordinatorReplacements(); err != nil || len(open) != 1 || open[0].ID != id {
		t.Fatalf("open=%+v err=%v", open, err)
	}
	if last, found, err := r.LastCoordinatorReplacement(spec.ProjectID); err != nil || !found || last.ID != id {
		t.Fatalf("last=%+v found=%v err=%v", last, found, err)
	}
	if acknowledged, err := r.AcknowledgeCoordinatorReplacement(id, now); err != nil || !acknowledged {
		t.Fatalf("acknowledged=%v err=%v", acknowledged, err)
	}
	if again, err := r.AcknowledgeCoordinatorReplacement(id, now); err != nil || again {
		t.Fatalf("acknowledged twice=%v err=%v", again, err)
	}
	if open, err := r.OpenCoordinatorReplacements(); err != nil || len(open) != 0 {
		t.Fatalf("open after ack=%+v err=%v", open, err)
	}
}

func TestAPersonsTakeoverIsNotARestartToAnnounce(t *testing.T) {
	r := coordinatorRegistry(t, "first", "second")
	spec := CoordinatorSpec{ProjectID: "project-work", ProjectRoot: "/work", Name: "work-lead", Surface: "claude", Cwd: "/work"}
	if _, err := r.SetCoordinator(spec, CoordinatorChange{SessionID: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(spec, CoordinatorChange{SessionID: "second", Replace: true, ChangedBy: "the CLI"}); err != nil {
		t.Fatal(err)
	}
	if unannounced, err := r.UnannouncedCoordinatorReplacements(); err != nil || len(unannounced) != 0 {
		t.Fatalf("unannounced=%+v err=%v", unannounced, err)
	}
	if open, err := r.OpenCoordinatorReplacements(); err != nil || len(open) != 0 {
		t.Fatalf("open=%+v err=%v", open, err)
	}
}

func TestCoordinatorTenuresSayWhenASessionHeldTheName(t *testing.T) {
	r := coordinatorRegistry(t, "first", "second")
	spec := CoordinatorSpec{ProjectID: "project-work", ProjectRoot: "/work", Name: "work-lead", Surface: "claude", Cwd: "/work"}
	if _, err := r.SetCoordinator(spec, CoordinatorChange{SessionID: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.HandOverCoordinator(spec, "first", "second", CoordinatorHandover{ChangedBy: "daemon", Reason: "gone: x"}); err != nil {
		t.Fatal(err)
	}
	first, err := r.CoordinatorTenures("first")
	if err != nil || len(first) != 1 || first[0].Name != "work-lead" || first[0].From.IsZero() || first[0].Until.IsZero() || first[0].Until.Before(first[0].From) {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := r.CoordinatorTenures("second")
	if err != nil || len(second) != 1 || second[0].Name != "work-lead" || !second[0].Until.IsZero() {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

func TestUpgradingDoesNotAnnounceHandoversThatHappenedBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO coordinator_history (project_id,action,name,session_id,previous_name,previous_session_id,changed_by,reason,at_ms) VALUES ('p','replaced','p-lead','second','p-lead','first','','gone: before',1000)`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`PRAGMA user_version=47`); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if r, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if unannounced, err := r.UnannouncedCoordinatorReplacements(); err != nil || len(unannounced) != 0 {
		t.Fatalf("unannounced=%+v err=%v", unannounced, err)
	}
	if open, err := r.OpenCoordinatorReplacements(); err != nil || len(open) != 0 {
		t.Fatalf("open=%+v err=%v", open, err)
	}
}
