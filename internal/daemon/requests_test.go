package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func deviceToken(t *testing.T, r *registry.Registry, name string, scopes ...string) string {
	t.Helper()
	pairing, err := r.CreateDevicePairing(name, scopes, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := r.CompleteDevicePairing(pairing.Secret, name, "")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func apiCall(t *testing.T, handler http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func historyOf(t *testing.T, r *registry.Registry, sessionID, kind string) []registry.HistoryEntry {
	t.Helper()
	entries, err := r.ListHistory(200, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var out []registry.HistoryEntry
	for _, entry := range entries {
		if entry.Kind == kind {
			out = append(out, entry)
		}
	}
	return out
}

func TestAPhoneAnswersAHostSessionsPermissionRequestOnceAndHistoryNamesIt(t *testing.T) {
	fixture := claudehosttest.New(t, "")
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	claude := surfaces.NewClaude(nil, fixture.Home)
	session := hostedSession(t, claude, id)
	r := hostRegistry(t)
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d := New(r, []surface.Surface{claude})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	phone := deviceToken(t, r, "Phone", "read", "control")
	reader := deviceToken(t, r, "Watcher", "read")

	if response := apiCall(t, handler, http.MethodPost, "/api/v1/actions", phone, map[string]any{"action": "send", "sessionId": id, "message": "TOOL", "author": "person"}); response.Code != http.StatusOK {
		t.Fatalf("send status=%d body=%s", response.Code, response.Body.String())
	}
	var state struct {
		PermissionMode string            `json:"permissionMode"`
		Requests       []surface.Request `json:"requests"`
	}
	claudehosttest.WaitFor(t, "the open request", func() bool {
		response := apiCall(t, handler, http.MethodGet, "/api/v1/requests?sessionId="+id, reader, nil)
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil {
			t.Fatalf("requests status=%d body=%s", response.Code, response.Body.String())
		}
		return len(state.Requests) == 1
	})
	request := state.Requests[0]
	stored, err := r.Session(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.observeTurn(context.Background(), claude, stored); err != nil {
		t.Fatal(err)
	}
	if waiting, _, err := r.WaitingState(id); err != nil || waiting == nil || waiting.ID != request.ID || waiting.Kind != surface.WaitingApproval {
		t.Fatalf("waiting = %+v, %v, want request %s", waiting, err, request.ID)
	}

	respond := map[string]any{"action": "respond", "sessionId": id, "requestId": request.ID, "decision": map[string]any{"behavior": "allow"}}
	assertAPIV1Error(t, apiCall(t, handler, http.MethodPost, "/api/v1/actions", reader, respond), http.StatusUnauthorized, "unauthorized")
	response := apiCall(t, handler, http.MethodPost, "/api/v1/actions", phone, respond)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"outcome":"allowed"`)) {
		t.Fatalf("respond status=%d body=%s", response.Code, response.Body.String())
	}
	if waiting, _, err := r.WaitingState(id); err != nil || waiting != nil {
		t.Fatalf("waiting after the answer = %+v, %v", waiting, err)
	}
	assertAPIV1Error(t, apiCall(t, handler, http.MethodPost, "/api/v1/actions", phone, respond), http.StatusConflict, "stale")
	claudehosttest.WaitFor(t, "the allowed turn", func() bool {
		turns := fixture.Turns(t, id)
		return len(turns) == 1 && turns[0].Reply == "tool:allow:"
	})
	answered := historyOf(t, r, id, "request-allowed")
	if len(answered) != 1 || answered[0].Message != request.ID || answered[0].Result != "answered by device Phone" {
		t.Fatalf("history = %+v", answered)
	}
}

func TestAPermissionModeChangeTakesTheCreationGuardAndIsRecordedWithWhoChangedIt(t *testing.T) {
	fixture := claudehosttest.New(t, "")
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	claude := surfaces.NewClaude(nil, fixture.Home)
	session := hostedSession(t, claude, id)
	r := hostRegistry(t)
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d := New(r, []surface.Surface{claude})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	phone := deviceToken(t, r, "Phone", "read", "control")
	reader := deviceToken(t, r, "Watcher", "read")

	change := map[string]any{"action": "permission-mode", "sessionId": id, "permissionMode": "bypassPermissions"}
	assertAPIV1Error(t, apiCall(t, handler, http.MethodPost, "/api/v1/actions", reader, change), http.StatusUnauthorized, "unauthorized")
	if response := apiCall(t, handler, http.MethodPost, "/api/v1/actions", phone, map[string]any{"action": "permission-mode", "sessionId": id, "permissionMode": "yolo"}); response.Code == http.StatusOK {
		t.Fatalf("an unknown mode was accepted: %s", response.Body.String())
	}
	if response := apiCall(t, handler, http.MethodPost, "/api/v1/actions", phone, change); response.Code != http.StatusOK {
		t.Fatalf("permission-mode status=%d body=%s", response.Code, response.Body.String())
	}
	if response := apiCall(t, handler, http.MethodPost, "/api/v1/actions", "secret", map[string]any{"action": "model", "sessionId": id, "model": "opus"}); response.Code != http.StatusOK {
		t.Fatalf("model status=%d body=%s", response.Code, response.Body.String())
	}
	response := apiCall(t, handler, http.MethodGet, "/api/v1/requests?sessionId="+id, reader, nil)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"permissionMode":"bypassPermissions"`)) {
		t.Fatalf("requests status=%d body=%s", response.Code, response.Body.String())
	}
	if changed := historyOf(t, r, id, "permission-mode"); len(changed) != 1 || changed[0].Message != "bypassPermissions" || changed[0].Result != "changed by device Phone" {
		t.Fatalf("permission-mode history = %+v", changed)
	}
	if changed := historyOf(t, r, id, "model"); len(changed) != 1 || changed[0].Message != "opus" || changed[0].Result != "changed by the dashboard" {
		t.Fatalf("model history = %+v", changed)
	}
	if stored, err := r.Session(id); err != nil || stored.ConfiguredModel != "opus" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}

func TestAHostRequestAsksForACatalogPassAtOnce(t *testing.T) {
	fixture := claudehosttest.New(t, "")
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	d := New(hostRegistry(t), nil)
	d.SetClaudeHost(fixture.Manager(t, "test"))
	ctx, cancel := context.WithCancel(context.Background())
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		d.watchClaudeHostRequests(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-watched
	})
	time.Sleep(300 * time.Millisecond)
	select {
	case <-d.catalogNudge:
	default:
	}
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: id, UUID: uuid.NewString(), Text: "TOOL", Origin: &claudehost.Origin{Kind: "human"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-d.catalogNudge:
	case <-time.After(5 * time.Second):
		t.Fatal("an opened request did not ask for a catalog pass")
	}
}
