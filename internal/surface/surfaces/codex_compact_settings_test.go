package surfaces

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type memoryCodexConfig struct {
	mu        sync.Mutex
	overrides map[string]map[string]any
}

func newMemoryCodexConfig() *memoryCodexConfig {
	return &memoryCodexConfig{overrides: map[string]map[string]any{}}
}

func (m *memoryCodexConfig) CodexConfigOverrides(id string) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	overrides := maps.Clone(m.overrides[id])
	if overrides == nil {
		overrides = map[string]any{}
	}
	return overrides, nil
}

func (m *memoryCodexConfig) SetCodexConfigOverride(id, key string, value any) (map[string]any, error) {
	previous, _ := m.CodexConfigOverrides(id)
	overrides := maps.Clone(previous)
	if value == nil {
		delete(overrides, key)
	} else {
		overrides[key] = value
	}
	return previous, m.RestoreCodexConfigOverrides(id, overrides)
}

func (m *memoryCodexConfig) RestoreCodexConfigOverrides(id string, overrides map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.overrides[id] = maps.Clone(overrides)
	return nil
}

func resumeAgain(t *testing.T, codex *Codex, session *surface.Session) {
	t.Helper()
	if _, err := codex.codexThreadRequest(context.Background(), mustRoute(t, codex, session), codexThreadResume, session, map[string]any{"threadId": session.ID}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestACodexCompactWindowReloadsTheThreadAndEveryLaterLoadCarriesIt(t *testing.T) {
	fake, codex, session := settingsThread(t, settingsModels)
	codex.SetCodexConfigStore(newMemoryCodexConfig())
	ctx := context.Background()

	if err := codex.SetCompactSetting(ctx, session, surface.CompactSettingWindow, "5000"); !errors.Is(err, surface.ErrInvalidTurnSetting) {
		t.Fatalf("a window out of range was accepted: %v", err)
	}
	if err := codex.SetCompactSetting(ctx, session, surface.CompactSettingWindow, "150000"); err != nil {
		t.Fatal(err)
	}
	if got := fake.LoadedConfig(settingsThreadID)[codexCompactLimit]; got != float64(150000) {
		t.Fatalf("the thread reloaded with config %v", fake.LoadedConfig(settingsThreadID))
	}
	settings, err := codex.CompactSettings(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if window := settingValue(t, settings, surface.CompactSettingWindow); window.Value != "150000" || window.Applies != surface.TurnSettingAppliesNextLoad {
		t.Fatalf("window = %+v", window)
	}

	fake.Unload(settingsThreadID)
	resumeAgain(t, codex, session)
	if got := fake.LoadedConfig(settingsThreadID)[codexCompactLimit]; got != float64(150000) {
		t.Fatalf("a later load carried config %v", fake.LoadedConfig(settingsThreadID))
	}

	if err := codex.SetCompactSetting(ctx, session, surface.CompactSettingWindow, surface.CompactAuto); err != nil {
		t.Fatal(err)
	}
	if _, set := fake.LoadedConfig(settingsThreadID)[codexCompactLimit]; set {
		t.Fatalf("Auto still reloads with a limit: %v", fake.LoadedConfig(settingsThreadID))
	}
}

func TestACodexReloadDropsAndRestoresAgenthailsOwnSubscriptions(t *testing.T) {
	fake, codex, session := settingsThread(t, settingsModels)
	codex.SetCodexConfigStore(newMemoryCodexConfig())
	ctx := context.Background()
	follower := mustRoute(t, codex, session)
	if !codex.subscribeManagedThread(ctx, follower, settingsThreadID) {
		t.Fatal("the follower did not subscribe")
	}
	resumesBefore := len(fake.Calls("thread/resume"))
	if err := codex.SetCompactSetting(ctx, session, surface.CompactSettingWindow, "200000"); err != nil {
		t.Fatal(err)
	}
	if got := len(fake.Calls("thread/unsubscribe")); got != 2 {
		t.Fatalf("unsubscribes = %d, want the follower's and the route's", got)
	}
	resumes := fake.Calls("thread/resume")[resumesBefore:]
	if len(resumes) != 2 || resumes[len(resumes)-1]["excludeTurns"] != true {
		t.Fatalf("resumes after the change = %v; the follower should follow the thread again last", resumes)
	}
	if got := codex.subscriptions.clients(settingsThreadID); len(got) != 1 {
		t.Fatalf("followers after the reload = %d", len(got))
	}
}

func TestAResumeWaitsWhileItsThreadReloads(t *testing.T) {
	fake, codex, session := settingsThread(t, settingsModels)
	follower := mustRoute(t, codex, session)
	release := codex.subscriptions.exclusive(settingsThreadID)
	before := len(fake.Calls("thread/resume"))
	done := make(chan bool, 1)
	go func() { done <- codex.subscribeManagedThread(context.Background(), follower, settingsThreadID) }()
	select {
	case <-done:
		t.Fatal("a follower subscribed while the thread reloaded")
	case <-time.After(300 * time.Millisecond):
	}
	if got := len(fake.Calls("thread/resume")); got != before {
		t.Fatalf("a resume reached Codex during the reload: %d resumes, want %d", got, before)
	}
	release()
	if !<-done {
		t.Fatal("the follower did not subscribe once the reload ended")
	}
}

func TestAFollowerThatCannotFollowAgainAfterAReloadIsClosed(t *testing.T) {
	var refuse atomic.Bool
	_, codex, session := settingsThread(t, func(method string, params map[string]any) map[string]any {
		if method == "thread/resume" && refuse.Load() && params["excludeTurns"] == true {
			return map[string]any{"error": map[string]any{"code": -32600, "message": "resume refused"}}
		}
		return settingsModels(method, params)
	})
	codex.SetCodexConfigStore(newMemoryCodexConfig())
	follower := mustRoute(t, codex, session)
	if !codex.subscribeManagedThread(context.Background(), follower, settingsThreadID) {
		t.Fatal("the follower did not subscribe")
	}
	refuse.Store(true)
	if err := codex.SetCompactSetting(context.Background(), session, surface.CompactSettingWindow, "200000"); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); !follower.(failingClient).failed(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a follower left without the thread's notifications stayed open")
		}
	}
	if slices.Contains(codex.subscriptions.clients(settingsThreadID), follower) {
		t.Fatal("a closed follower is still counted as following the thread")
	}
}

func TestAFollowerAfterAFailedReloadJoinsWithoutTheRefusedWindow(t *testing.T) {
	var refuse atomic.Bool
	fake, codex, session := settingsThread(t, func(method string, params map[string]any) map[string]any {
		if method == "thread/resume" && refuse.Load() && params["excludeTurns"] != true {
			return map[string]any{"error": map[string]any{"code": -32600, "message": "resume refused"}}
		}
		return settingsModels(method, params)
	})
	codex.SetCodexConfigStore(newMemoryCodexConfig())
	ctx := context.Background()
	if err := codex.SetCompactSetting(ctx, session, surface.CompactSettingWindow, "150000"); err != nil {
		t.Fatal(err)
	}
	follower := mustRoute(t, codex, session)
	if !codex.subscribeManagedThread(ctx, follower, settingsThreadID) {
		t.Fatal("the follower did not subscribe")
	}
	fake.HoldElsewhere(settingsThreadID, true)
	refuse.Store(true)
	if err := codex.SetCompactSetting(ctx, session, surface.CompactSettingWindow, "300000"); err == nil {
		t.Fatal("a reload that failed reported success")
	}
	resumes := fake.Calls("thread/resume")
	last := resumes[len(resumes)-1]
	if last["excludeTurns"] != true || last["config"] != nil {
		t.Fatalf("the follower resumed with %v after the reload failed", last)
	}
}

func TestAReloadCodexDoesNotAnswerKeepsTheNewWindow(t *testing.T) {
	var stall atomic.Bool
	fake, codex, session := settingsThread(t, func(method string, params map[string]any) map[string]any {
		if method == "thread/resume" && stall.Load() && params["excludeTurns"] != true {
			time.Sleep(500 * time.Millisecond)
		}
		return settingsModels(method, params)
	})
	store := newMemoryCodexConfig()
	codex.SetCodexConfigStore(store)
	wait := codexReloadWait
	codexReloadWait = 100 * time.Millisecond
	t.Cleanup(func() { codexReloadWait = wait })
	stall.Store(true)
	if err := codex.SetCompactSetting(context.Background(), session, surface.CompactSettingWindow, "300000"); err == nil {
		t.Fatal("a reload Codex never answered reported success")
	}
	if overrides, _ := store.CodexConfigOverrides(settingsThreadID); overrides[codexCompactLimit] != int64(300000) {
		t.Fatalf("a reload Codex may have taken was undone: %v", overrides)
	}
	for deadline := time.Now().Add(5 * time.Second); fake.LoadedConfig(settingsThreadID)[codexCompactLimit] != float64(300000); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("Codex loaded %v", fake.LoadedConfig(settingsThreadID))
		}
	}
}

func TestACodexReloadThatFailsKeepsTheOldWindow(t *testing.T) {
	fail := false
	fake, codex, session := settingsThread(t, func(method string, params map[string]any) map[string]any {
		if method == "thread/resume" && fail {
			return map[string]any{"error": map[string]any{"code": -32600, "message": "resume refused"}}
		}
		return settingsModels(method, params)
	})
	store := newMemoryCodexConfig()
	codex.SetCodexConfigStore(store)
	ctx := context.Background()
	if err := codex.SetCompactSetting(ctx, session, surface.CompactSettingWindow, "150000"); err != nil {
		t.Fatal(err)
	}
	fake.Load(settingsThreadID)
	fail = true
	if err := codex.SetCompactSetting(ctx, session, surface.CompactSettingWindow, "300000"); err == nil {
		t.Fatal("a reload that failed reported success")
	}
	if overrides, _ := store.CodexConfigOverrides(settingsThreadID); overrides[codexCompactLimit] != int64(150000) {
		t.Fatalf("after a failed reload the thread keeps %v", overrides)
	}
}

func TestACodexCompactWindowIsRefusedWhileTheThreadWorks(t *testing.T) {
	_, codex, session := settingsThread(t, settingsModels)
	store := newMemoryCodexConfig()
	codex.SetCodexConfigStore(store)
	session.Status = surface.StatusBusy
	if err := codex.SetCompactSetting(context.Background(), session, surface.CompactSettingWindow, "200000"); !errors.Is(err, surface.ErrInvalidTurnSetting) {
		t.Fatalf("err = %v", err)
	}
	if overrides, _ := store.CodexConfigOverrides(session.ID); len(overrides) != 0 {
		t.Fatalf("a refused change was stored: %v", overrides)
	}
}

func mustRoute(t *testing.T, codex *Codex, session *surface.Session) codexClient {
	t.Helper()
	client, err := codex.openRoute(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}
