package approval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeClaudePermissionRequest(t *testing.T) {
	now := time.Now()
	request, err := Normalize([]byte(`{"source":"claude-code","session_id":"session-1","tool_name":"Bash","tool_input":{"command":"git status"}}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if request.ID == "" || !strings.HasPrefix(request.ID, "apr_") || request.Source != "claude-code" || request.Tool != "Bash" || request.Action != "Bash" {
		t.Fatalf("request=%+v", request)
	}
	if request.ExpiresAt.Sub(request.CreatedAt) != DefaultTTL {
		t.Fatalf("ttl=%s", request.ExpiresAt.Sub(request.CreatedAt))
	}
	var input map[string]string
	if err := json.Unmarshal(request.Input, &input); err != nil || input["command"] != "git status" {
		t.Fatalf("input=%s err=%v", request.Input, err)
	}
}

func TestNormalizeCanonicalizesInputForStableRequestID(t *testing.T) {
	first, err := Normalize([]byte(`{"source":"test","tool":"Bash","input":{"command":"git push","cwd":"/tmp"}}`), time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Normalize([]byte(`{"source":"test","tool":"Bash","input":{"cwd":"/tmp","command":"git push"}}`), time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || string(first.Input) != string(second.Input) {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestNormalizeRejectsMalformedAndOversizedRequests(t *testing.T) {
	if _, err := Normalize([]byte(`{} trailing`), time.Now()); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	input := strings.Repeat("x", MaxInputBytes+1)
	if _, err := Normalize([]byte(`{"tool":"Bash","input":"`+input+`"}`), time.Now()); err == nil {
		t.Fatal("oversized input accepted")
	}
}

func TestObserveFailsClosedUntilExplicitDecision(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "approvals.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	request, err := Normalize([]byte(`{"request_id":"request-1","source":"test","tool":"Bash","input":{"command":"git push"}}`), now)
	if err != nil {
		t.Fatal(err)
	}
	response, created, err := store.Observe(request, now)
	if err != nil || !created || response.Decision != DecisionPending {
		t.Fatalf("response=%+v created=%v err=%v", response, created, err)
	}
	if err := store.Decide(request.ID, DecisionAllow, "approved", now); err != nil {
		t.Fatal(err)
	}
	response, created, err = store.Observe(request, now)
	if err != nil || created || response.Decision != DecisionAllow || response.Reason != "approved" {
		t.Fatalf("response=%+v created=%v err=%v", response, created, err)
	}
	rows, err := store.List(10)
	if err != nil || len(rows) != 1 || rows[0].Decision != DecisionAllow {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestExpiredApprovalCannotBeAllowed(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "approvals.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Unix(100, 0)
	request, err := Normalize([]byte(`{"request_id":"expires","tool":"Bash"}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Observe(request, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Decide(request.ID, DecisionAllow, "late", now.Add(DefaultTTL+time.Second)); err == nil {
		t.Fatal("expired request was allowed")
	}
	response, reopened, err := store.Observe(request, now.Add(DefaultTTL+time.Second))
	if err != nil || !reopened || response.Decision != DecisionPending {
		t.Fatalf("reopened response=%+v reopened=%v err=%v", response, reopened, err)
	}
	rows, err := store.List(10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestApprovalStoreIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "approvals.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func TestApprovalStorePrunesRows(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "approvals.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	for i := 0; i < MaxRows+5; i++ {
		request, normalizeErr := Normalize([]byte(fmt.Sprintf(`{"request_id":"row-%d","tool":"Bash"}`, i)), now)
		if normalizeErr != nil {
			t.Fatal(normalizeErr)
		}
		if _, _, observeErr := store.Observe(request, now); observeErr != nil {
			t.Fatal(observeErr)
		}
	}
	if _, err := store.List(MaxListLimit); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM approval_requests`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != MaxRows {
		t.Fatalf("rows=%d want=%d", count, MaxRows)
	}
}
