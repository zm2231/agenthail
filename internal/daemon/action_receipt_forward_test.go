package daemon

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/hosts"
)

func (n *federationNode) keyedAction(t *testing.T, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(encoded))
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	request.Header.Set(actionIdempotencyHeader, key)
	request.AddCookie(browserCookie(t, n.reg))
	response := httptest.NewRecorder()
	n.handler.ServeHTTP(response, request)
	return response
}

func TestAResendAfterAnUnansweredForwardLearnsTheHostsOutcomeWithoutRunningTwice(t *testing.T) {
	local, remote, record := pairedFederation(t)
	action := map[string]string{"action": "interrupt", "sessionId": hosts.Qualify(record.ID, "remote-a")}
	remote.gate.drops.Store(2)
	lost := local.keyedAction(t, "stop-once", action)
	if lost.Code != http.StatusGatewayTimeout || apiErrorCode(t, lost) != "host_outcome_unknown" {
		t.Fatalf("lost answer status=%d body=%s", lost.Code, lost.Body.String())
	}
	remote.gate.drops.Store(0)
	if got := remote.fake.interrupts(); got != 1 {
		t.Fatalf("remote interrupts after the lost answer=%d", got)
	}
	learned := local.keyedAction(t, "stop-once", action)
	if learned.Code != http.StatusOK {
		t.Fatalf("resend status=%d body=%s, want the host's stored outcome", learned.Code, learned.Body.String())
	}
	again := local.keyedAction(t, "stop-once", action)
	if again.Code != learned.Code || again.Body.String() != learned.Body.String() {
		t.Fatalf("later resend=%d %s, want %d %s", again.Code, again.Body.String(), learned.Code, learned.Body.String())
	}
	if got := remote.fake.interrupts(); got != 1 {
		t.Fatalf("resends ran the interrupt again: interrupts=%d", got)
	}
}

func TestConcurrentResendsAfterAnUnansweredForwardRunOnceAndAgree(t *testing.T) {
	local, remote, record := pairedFederation(t)
	action := map[string]string{"action": "interrupt", "sessionId": hosts.Qualify(record.ID, "remote-a")}
	remote.gate.drops.Store(2)
	if lost := local.keyedAction(t, "stop-race", action); lost.Code != http.StatusGatewayTimeout {
		t.Fatalf("lost answer status=%d body=%s", lost.Code, lost.Body.String())
	}
	remote.gate.drops.Store(0)
	var group sync.WaitGroup
	answers := make([]*httptest.ResponseRecorder, 2)
	for i := range answers {
		group.Add(1)
		go func() {
			defer group.Done()
			answers[i] = local.keyedAction(t, "stop-race", action)
		}()
	}
	group.Wait()
	for _, answer := range answers {
		if answer.Code != http.StatusOK && answer.Code != http.StatusAccepted {
			t.Fatalf("concurrent resend status=%d body=%s, want the outcome or still running", answer.Code, answer.Body.String())
		}
	}
	final := local.keyedAction(t, "stop-race", action)
	if final.Code != http.StatusOK {
		t.Fatalf("resend after the race status=%d body=%s", final.Code, final.Body.String())
	}
	if got := remote.fake.interrupts(); got != 1 {
		t.Fatalf("interrupts=%d, want 1", got)
	}
}

// A forwarded channel message carries the time it was sent and the person's
// name; a resend has to forward the same body, or the host refuses the key as
// a different request.
func TestAResendForwardsTheSameBodyItFirstSent(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	if err := local.reg.SetPersonName("Ada"); err != nil {
		t.Fatal(err)
	}
	action := map[string]string{"action": "channel-send", "host": "vps", "channel": "release", "message": "ship it"}
	remote.gate.drops.Store(2)
	if lost := local.keyedAction(t, "channel-once", action); lost.Code != http.StatusGatewayTimeout {
		t.Fatalf("lost answer status=%d body=%s", lost.Code, lost.Body.String())
	}
	remote.gate.drops.Store(0)
	if err := local.reg.SetPersonName("Ada Lovelace"); err != nil {
		t.Fatal(err)
	}
	resend := local.keyedAction(t, "channel-once", action)
	if resend.Code == http.StatusGatewayTimeout || apiErrorCode(t, resend) == "action_idempotency_mismatch" {
		t.Fatalf("resend status=%d body=%s, want the host's stored outcome", resend.Code, resend.Body.String())
	}
}

func TestAResendGoesToTheHostThatMayHaveRunItEvenAfterTheRouteChanged(t *testing.T) {
	local, remote, record := pairedFederation(t)
	action := map[string]string{"action": "interrupt", "sessionId": "remote-a"}
	remote.gate.drops.Store(2)
	lost := local.keyedAction(t, "stop-moved", action)
	if lost.Code != http.StatusGatewayTimeout {
		t.Fatalf("lost answer status=%d body=%s", lost.Code, lost.Body.String())
	}
	remote.gate.drops.Store(0)
	db, err := sql.Open("sqlite", local.reg.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=10000`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sessions SET host_id='' WHERE id=? OR id=?`, "remote-a", hosts.Qualify(record.ID, "remote-a")); err != nil {
		t.Fatal(err)
	}
	learned := local.keyedAction(t, "stop-moved", action)
	if learned.Code != http.StatusOK {
		t.Fatalf("resend status=%d body=%s, want the first host's stored outcome", learned.Code, learned.Body.String())
	}
	if remote.fake.interrupts() != 1 || local.fake.interrupts() != 0 {
		t.Fatalf("interrupts remote=%d local=%d, want the first host's one run only", remote.fake.interrupts(), local.fake.interrupts())
	}
}
