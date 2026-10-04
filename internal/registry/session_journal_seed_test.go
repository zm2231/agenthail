package registry

import "testing"

func TestSessionJournalSeedStatusPersistsEmptySuccessAndFailedRetryState(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	status, err := r.SessionJournalSeedStatus("session")
	if err != nil || status != SessionJournalSeedUnknown {
		t.Fatalf("initial status=%q err=%v", status, err)
	}
	if err := r.MarkSessionJournalSeed("session", true); err != nil {
		t.Fatal(err)
	}
	status, err = r.SessionJournalSeedStatus("session")
	if err != nil || status != SessionJournalSeeded {
		t.Fatalf("success status=%q err=%v", status, err)
	}
	if err := r.MarkSessionJournalSeed("session", false); err != nil {
		t.Fatal(err)
	}
	status, err = r.SessionJournalSeedStatus("session")
	if err != nil || status != SessionJournalSeedFailed {
		t.Fatalf("failed status=%q err=%v", status, err)
	}
}
