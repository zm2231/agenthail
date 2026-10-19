package daemon

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestARefusalThatReturnsWhileTheFirstIsStillAnnouncedIsAnnouncedAgain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	pushes := newPushRecorder(t, store)
	stall := make(chan struct{})
	pushes.mu.Lock()
	pushes.stall = stall
	pushes.mu.Unlock()
	released := false
	release := func() {
		if !released {
			released = true
			close(stall)
		}
	}
	t.Cleanup(release)
	target := surface.Session{ID: "target", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle}
	if err := store.RegisterSession(target); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRuntimeState(target.ID, surface.TurnObservation{Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	id, err := store.QueueMessageWithOptions(target.ID, "hello", "", surface.SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d := New(store, nil)
	now := time.Now()
	attempt := func(cause error) {
		t.Helper()
		now = now.Add(time.Hour)
		item, err := store.ClaimNextMessage(target.ID, now)
		if err != nil || item == nil || item.ID != id {
			t.Fatalf("claim=%+v err=%v, want %d", item, err, id)
		}
		d.finishQueueFailure(item, &target, cause, now)
	}
	refused := surface.DeliveryRefused(errors.New("Claude refused to resume target: Workspace not trusted"))

	attempt(refused)
	eventually(t, "the first refusal's announcement", func() bool { return pushes.count("delivery.refused") == 1 })
	attempt(surface.DeliveryUnavailable(errors.New("Claude did not resume target: Claude did not start")))
	attempt(refused)
	eventually(t, "the second refusal's announcement", func() bool { return pushes.count("delivery.refused") == 2 })
	settled := func() bool {
		d.announceMu.Lock()
		defer d.announceMu.Unlock()
		return len(d.announcingRefusal) == 0
	}
	release()
	eventually(t, "both announcements to finish", settled)
	attempt(refused)
	eventually(t, "any further announcement to finish", settled)
	if got := pushes.count("delivery.refused"); got != 2 {
		t.Fatalf("refusal announcements=%d, want one per refusal", got)
	}
}
