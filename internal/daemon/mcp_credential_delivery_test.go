package daemon

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func credentialRows(t *testing.T, r *registry.Registry, sessionID string) int {
	t.Helper()
	var rows int
	if err := r.WriteDB().QueryRow(`SELECT COUNT(*) FROM session_credentials WHERE session_id=?`, sessionID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func liveDelivered(t *testing.T, r *registry.Registry, sessionID string) bool {
	t.Helper()
	live, err := r.LiveCredentials()
	if err != nil {
		t.Fatal(err)
	}
	return live[sessionID].Delivered
}

func TestARuntimeThatRefusesItsNewCredentialGetsTheSameTokenRetriedNotANewOne(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_MCP_SET_ERROR", "refused")
	f := newMCPClaudeFixture(t)
	f.addProject(t)
	id, _ := f.create(t)
	f.action(t, fmt.Sprintf(`{"action":"coordinator-set","sessionId":%q,"name":"captain"}`, id))
	for range 10 {
		f.daemon.syncLeads()
	}
	if rows := credentialRows(t, f.registry, id); rows != 2 {
		t.Fatalf("credentials=%d, want the first and exactly one rotation", rows)
	}
	attempts := f.replacedTokens(t, id)
	if len(attempts) < 11 || len(slices.Compact(slices.Clone(attempts))) != 1 {
		t.Fatalf("delivery attempts=%d distinct tokens=%d, want every tick to retry the one rotated token", len(attempts), len(slices.Compact(slices.Clone(attempts))))
	}
	if liveDelivered(t, f.registry, id) {
		t.Fatal("a refused token was recorded as delivered")
	}
}

func TestAnUndeliveredCredentialAfterARestartIsMintedOnceThenRetried(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_MCP_SET_ERROR", "refused")
	f := newMCPClaudeFixture(t)
	f.addProject(t)
	id, _ := f.create(t)
	f.action(t, fmt.Sprintf(`{"action":"coordinator-set","sessionId":%q,"name":"captain"}`, id))
	f.daemon = newTestDaemon(t, f.registry, f.daemon.Surfaces)
	f.daemon.attachMCPCredentials(f.server.Listener.Addr().String())
	for range 10 {
		f.daemon.syncLeads()
	}
	if rows := credentialRows(t, f.registry, id); rows != 3 {
		t.Fatalf("credentials=%d, want one more mint after the restart and none after", rows)
	}
	attempts := f.replacedTokens(t, id)
	afterRestart := attempts[len(attempts)-10:]
	if len(slices.Compact(slices.Clone(afterRestart))) != 1 || !slices.Contains(f.capabilities(t, afterRestart[0]), any("control.project")) {
		t.Fatal("the restarted daemon did not keep retrying its one live lead token")
	}
}

func TestAConversationNoHostHoldsIsDeliveredByItsNextOpen(t *testing.T) {
	f := newMCPClaudeFixture(t)
	f.addProject(t)
	id, _ := f.create(t)
	f.host.KillHost(t)
	f.action(t, fmt.Sprintf(`{"action":"coordinator-set","sessionId":%q,"name":"captain"}`, id))
	f.daemon.syncLeads()
	if !liveDelivered(t, f.registry, id) {
		t.Fatal("a conversation no host holds was left undelivered; its next open carries the live token")
	}
}

type fakeRuntime struct {
	mu        sync.Mutex
	installed string
}

func (r *fakeRuntime) install(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.installed = token
}

func (r *fakeRuntime) token() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.installed
}

func TestALateOlderPushNeverLeavesARevokedTokenInstalled(t *testing.T) {
	f := newMCPClaudeFixture(t)
	id, _ := f.create(t)
	credentials := f.daemon.mcpCredentials.Load()
	if _, err := credentials.Rotate(id); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	blocked, release := make(chan struct{}), make(chan struct{})
	first := true
	done := make(chan error, 2)
	go func() {
		done <- credentials.Deliver(id, "", func(token string) error {
			if first {
				first = false
				close(blocked)
				<-release
			}
			runtime.install(token)
			return nil
		})
	}()
	<-blocked
	newer, err := credentials.Rotate(id)
	if err != nil {
		t.Fatal(err)
	}
	go func() { done <- credentials.Deliver(id, "", func(token string) error { runtime.install(token); return nil }) }()
	time.Sleep(50 * time.Millisecond)
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if runtime.token() != newer {
		t.Fatal("the runtime ended on a revoked token")
	}
	if !liveDelivered(t, f.registry, id) {
		t.Fatal("the live token was not recorded as delivered")
	}
}

func TestAResumeThatRacedARotationEndsOnTheLiveToken(t *testing.T) {
	f := newMCPClaudeFixture(t)
	id, _ := f.create(t)
	credentials := f.daemon.mcpCredentials.Load()
	opened, err := credentials.Rotate(id)
	if err != nil {
		t.Fatal(err)
	}
	live, err := credentials.Rotate(id)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{installed: opened}
	if err := credentials.Deliver(id, opened, func(token string) error { runtime.install(token); return nil }); err != nil {
		t.Fatal(err)
	}
	if runtime.token() != live || !liveDelivered(t, f.registry, id) {
		t.Fatal("a resume that opened with a token rotated away under it was not moved to the live token")
	}
	if rows := credentialRows(t, f.registry, id); rows != 3 {
		t.Fatalf("credentials=%d, confirming a resume must not mint", rows)
	}
}
