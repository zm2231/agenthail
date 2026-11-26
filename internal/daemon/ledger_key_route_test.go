package daemon

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
)

func TestLedgerRoutesTakeTheSnapshotProjectID(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	notes := addLedgerProject(t, r, "p-notes", "")
	widget := addLedgerProject(t, r, "p-widget", "repo:github.com/acme/widget")
	late := addLedgerProject(t, r, "p-late", "")
	notes.session(t, r, "s-notes")
	widget.session(t, r, "s-widget")
	late.session(t, r, "s-late")

	add := func(projectID, sessionID, text string) {
		t.Helper()
		var added ledger.WorkResult
		if code, body := ledgerWrite(t, handler, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: projectID, SessionID: sessionID, Action: ledger.WorkAdd, Text: text}, &added); code != http.StatusOK {
			t.Fatalf("adding work to %s: %d %s", projectID, code, body)
		}
	}
	read := func(projectID string) (string, []string) {
		t.Helper()
		page, _ := ledgerGetJSON(t, handler, "/api/v1/ledger?view=work&projectKey="+url.QueryEscape(projectID))
		var texts []string
		items, _ := page["work"].([]any)
		for _, item := range items {
			texts = append(texts, item.(map[string]any)["text"].(string))
		}
		key, _ := page["projectKey"].(string)
		return key, texts
	}

	add("p-notes", "s-notes", "write the notes")
	add("repo:github.com/acme/widget", "s-widget", "build the widget")
	add("p-late", "s-late", "before the repository")
	if err := r.SetProjectRepository("p-late", "repo:github.com/acme/late", nil); err != nil {
		t.Fatal(err)
	}
	add("repo:github.com/acme/late", "s-late", "after the repository")

	for _, want := range []struct {
		id, key string
		texts   []string
	}{
		{"p-notes", registry.LedgerProjectKey("p-notes"), []string{"write the notes"}},
		{"repo:github.com/acme/widget", "repo:github.com/acme/widget", []string{"build the widget"}},
		{"repo:github.com/acme/late", registry.LedgerProjectKey("p-late"), []string{"before the repository", "after the repository"}},
		{registry.LedgerProjectKey("p-late"), registry.LedgerProjectKey("p-late"), []string{"before the repository", "after the repository"}},
	} {
		if key, texts := read(want.id); key != want.key || len(texts) != len(want.texts) || texts[0] != want.texts[0] || texts[len(texts)-1] != want.texts[len(want.texts)-1] {
			t.Errorf("reading %s = %s %v, want %s %v", want.id, key, texts, want.key, want.texts)
		}
	}
	if w := ledgerGet(t, handler, "/api/v1/ledger?projectKey=p-nowhere", ""); w.Code != http.StatusNotFound {
		t.Errorf("an id no project is listed under = %d %s", w.Code, w.Body)
	}

	snapshot := ledgerGet(t, handler, "/api/v1/snapshot?sessions=none", "")
	var base struct {
		Projects []struct {
			ID        string `json:"id"`
			LedgerKey string `json:"ledgerKey"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(snapshot.Body.Bytes(), &base); snapshot.Code != http.StatusOK || err != nil {
		t.Fatalf("snapshot %d %v", snapshot.Code, err)
	}
	listed := map[string]string{}
	for _, project := range base.Projects {
		listed[project.ID] = project.LedgerKey
	}
	for id, key := range map[string]string{"p-notes": registry.LedgerProjectKey("p-notes"), "repo:github.com/acme/widget": "repo:github.com/acme/widget", "repo:github.com/acme/late": registry.LedgerProjectKey("p-late")} {
		if listed[id] != key {
			t.Errorf("snapshot project %s has ledgerKey %q, want %q (projects %v)", id, listed[id], key, listed)
		}
	}
}
