package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func ledgerWrite(t *testing.T, handler http.Handler, path string, payload, into any) (int, string) {
	t.Helper()
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code == http.StatusOK && into != nil {
		if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code, w.Body.String()
}

func TestLedgerAPIAttachesCapturedWordsToAGoal(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-a", "repo:github.com/acme/widget")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	p.session(t, r, "s1")
	ref := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "s1", PromptID: "p-1", Prompt: "add a cart"}).Ref

	var goal ledgerGoalResponse
	if code, body := ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: p.key, SessionID: "s1", Name: "Checkout"}, &goal); code != http.StatusOK || !goal.Created || goal.Goal.ID != "widget-G1" {
		t.Fatalf("goal status=%d body=%s", code, body)
	}
	var result ledger.AttachResult
	code, body := ledgerWrite(t, handler, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: p.key, SessionID: "s1", UtteranceID: ref, As: ledger.AsNewAsk, To: "widget-G1", Clauses: []string{"add a cart"}}, &result)
	if code != http.StatusOK || result.Readback != "intent ledger: +widget-A1 on widget-G1 (Checkout) with 1 clause (widget-C1)" {
		t.Fatalf("attach status=%d body=%s", code, body)
	}
}

func TestLedgerAPICaptureNamesTheSessionsEarlierUnattachedWords(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-u", "repo:github.com/acme/widget")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	p.session(t, r, "s1")
	p.session(t, r, "s2")
	first := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "s1", PromptID: "p-1", Prompt: "hello"}).Ref
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "s1", PromptID: "p-2", Prompt: "add a cart"})
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "s2", PromptID: "p-3", Prompt: "other session"})
	if code, body := ledgerWrite(t, handler, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: p.key, SessionID: "s1", UtteranceID: first, As: ledger.AsConversation}, nil); code != http.StatusOK {
		t.Fatalf("attach status=%d body=%s", code, body)
	}
	resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "s1", PromptID: "p-4", Prompt: "and a receipt"})
	if len(resp.Unattached) != 1 || resp.Unattached[0] != ledger.PromptRef("p-2") {
		t.Fatalf("unattached = %v, want only the earlier unattached prompt of s1", resp.Unattached)
	}
}

func TestLedgerAPIWorkItemsMoveThroughTheDaemon(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-w", "repo:github.com/acme/widget")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	p.session(t, r, "s1")
	ref := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "s1", PromptID: "p-1", Prompt: "add search"}).Ref
	ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: p.key, SessionID: "s1", Name: "Search"}, nil)
	ledgerWrite(t, handler, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: p.key, SessionID: "s1", UtteranceID: ref, As: ledger.AsNewAsk, To: "widget-G1", Clauses: []string{"add search"}}, nil)
	var added ledger.WorkResult
	if code, body := ledgerWrite(t, handler, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "add the index"}, &added); code != http.StatusOK || added.Item.ID != "widget-W1" {
		t.Fatalf("add status=%d body=%s", code, body)
	}
	code, body := ledgerWrite(t, handler, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkSubmit, ItemID: "widget-W1"}, nil)
	if code != http.StatusConflict || !strings.Contains(body, "submit needs it in_progress") {
		t.Fatalf("done before claim: status=%d body=%s", code, body)
	}
	code, body = ledgerWrite(t, handler, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkLog, ItemID: "widget-W1", Text: "started"}, nil)
	if code != http.StatusConflict || !strings.Contains(body, "has no assignee") {
		t.Fatalf("log before claim: status=%d body=%s", code, body)
	}
	var commented ledger.WorkResult
	if code, body := ledgerWrite(t, handler, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkComment, ItemID: "widget-W1", Text: "check the migration"}, &commented); code != http.StatusOK || commented.Readback != "intent ledger: comment on widget-W1" {
		t.Fatalf("comment status=%d body=%s", code, body)
	}
	if n := len(commented.Item.Timeline); n != 2 || commented.Item.Timeline[1].Kind != "comment" {
		t.Fatalf("comment result timeline = %+v", commented.Item.Timeline)
	}
}

func TestLedgerAPIRefusesUnprovenClausesWithAConflict(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-b", "repo:github.com/acme/widget")
	p.session(t, r, "s1")
	ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: p.key, SessionID: "s1", Name: "Checkout"}, nil)
	code, body := ledgerWrite(t, handler, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: p.key, SessionID: "s1", As: ledger.AsNewAsk, To: "widget-G1", Clauses: []string{"add a cart"}}, nil)
	if code != http.StatusConflict || !strings.Contains(body, "stated clauses need the person's captured utterance") {
		t.Fatalf("status=%d body=%s", code, body)
	}
}

func TestLedgerAPIWritesNeedAKnownSession(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-c", "repo:github.com/acme/widget")
	if code, _ := ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: p.key, Name: "Checkout"}, nil); code != http.StatusBadRequest {
		t.Fatalf("no session: status %d, want 400", code)
	}
	if code, _ := ledgerWrite(t, handler, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: p.key, SessionID: "ghost", As: ledger.AsConversation}, nil); code != http.StatusNotFound {
		t.Fatalf("unknown session: status %d, want 404", code)
	}
	p.session(t, r, "s1")
	if code, body := ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: "repo:github.com/acme/typo", SessionID: "s1", Name: "Checkout"}, nil); code != http.StatusNotFound || !strings.Contains(body, "project_not_found") {
		t.Fatalf("unknown project: status %d body %s", code, body)
	}
}
