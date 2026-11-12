package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestANotificationWithdrawnWhileTheDaemonStopsLeavesTheDevicesWhenItStartsAgain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := surface.Session{ID: "asking-session", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle}
	if err := store.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	pushes := newPushRecorder(t, store)
	var input askInput
	if err := json.Unmarshal([]byte(`{"title":"Release today?","questions":[{"question":"Release today?","options":[{"label":"Yes"},{"label":"No"}]}]}`), &input); err != nil {
		t.Fatal(err)
	}

	before := &withdrawals{}
	first, stop := runDaemonWith(t, store, func(d *Daemon) { d.withdrawDesktop = before.record })
	ask, err := first.daemon.raiseAsk(session, input)
	if err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the ask on the person's phone", 5*time.Second, func() bool { return pushes.count("ask.opened") == 1 })
	stop()
	if err := first.daemon.withdrawOwnAsk(session, ask.ID); err != nil {
		t.Fatal(err)
	}
	key := askNotificationKey(ask.ID)
	if before.has("agenthail-"+key) || len(pushes.all("")) != 0 {
		t.Fatal("a stopped daemon withdrew the notification")
	}

	after := &withdrawals{}
	runDaemonWith(t, store, func(d *Daemon) { d.withdrawDesktop = after.record })
	testkit.Eventually(t, "the notification withdrawn from the desktop and the phone", 5*time.Second, func() bool {
		withdrawn := pushes.all("")
		return after.has("agenthail-"+key) && len(withdrawn) == 1 && withdrawn[0]["collapseId"] == key
	})
	testkit.Eventually(t, "the withdraw forgotten once sent", 5*time.Second, func() bool {
		pending, err := store.PendingNotificationWithdraws()
		return err == nil && len(pending) == 0
	})
}
