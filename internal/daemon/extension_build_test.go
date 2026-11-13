package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/poll"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestBuildStartsAPersonSessionInTheExtensionsFolderWithItsGuide(t *testing.T) {
	f := startExtensionFixture(t)
	workspace := extension.Workspace(f.home)

	response, body := f.request(t, http.MethodPost, "/api/v1/extensions/build", "secret", map[string]string{"message": "a page with my notes", "surface": "codex"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("build answered %d %s", response.StatusCode, body)
	}
	if len(f.fake.startOptions) != 1 {
		t.Fatalf("build started %d sessions", len(f.fake.startOptions))
	}
	started := f.fake.startOptions[0]
	if started.Cwd != workspace || started.PermissionMode != "" || started.ApprovalPolicy != "" || started.ConfigRoot != "" {
		t.Fatalf("the builder started with %+v", started)
	}
	if started.Message != extensionBuildLine+"\n\na page with my notes" {
		t.Fatalf("the builder was told %q", started.Message)
	}
	origin, found, err := f.d.Registry.SessionOrigin("started")
	if err != nil || !found || origin.Via != registry.OriginPerson || origin.CreatorID != "" {
		t.Fatalf("the builder's origin is %+v (found=%v, %v), not the person", origin, found, err)
	}

	for name, want := range map[string]string{"CLAUDE.md": "@AGENTS.md", "AGENTS.md": "agenthail extensions call", "recipes/keyed-api.md": "ah.fetch"} {
		data, err := os.ReadFile(filepath.Join(workspace, name))
		if err != nil || !strings.Contains(string(data), want) {
			t.Fatalf("%s holds %q (%v)", name, data, err)
		}
	}
	seeded := f.changes(t)[0]
	if seeded.Author != "agenthail" || len(seeded.Extensions) != 0 {
		t.Fatalf("the guide was recorded as %+v", seeded)
	}
	if view := f.d.extensionsView(); len(view.Extensions) != 1 {
		t.Fatalf("the recipes folder was taken for an extension: %+v", view.Extensions)
	}

	guide := filepath.Join(workspace, "AGENTS.md")
	if err := os.WriteFile(guide, []byte("my own rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.d.seedExtensionWorkspace(true); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(guide); string(data) != "my own rules\n" {
		t.Fatalf("seeding again replaced the person's edit with %q", data)
	}
	if err := os.Remove(filepath.Join(workspace, "recipes", "collector.md")); err != nil {
		t.Fatal(err)
	}
	if err := f.d.seedExtensionWorkspace(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "recipes", "collector.md")); err != nil {
		t.Fatalf("a missing recipe was not written again: %v", err)
	}
}

func (f *extensionFixture) call(t *testing.T, id string, request map[string]any) (int, extensionCallResult) {
	t.Helper()
	response, body := f.request(t, http.MethodPost, "/api/v1/extensions/"+id+"/call", "secret", request)
	var result extensionCallResult
	if response.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
	}
	return response.StatusCode, result
}

func TestACallMarksAChangeVerifiedOnlyForTheVersionItRan(t *testing.T) {
	f := startExtensionFixture(t)
	reg := f.d.Registry
	workspace := extension.Workspace(f.home)

	writeExtension(t, workspace, "notes", `{"label": "Notes"}`)
	f.turn(t, "to", "turn-1")
	ask, open := openChangeAsk(t, reg, "to")
	if !open || !strings.Contains(ask.Questions[0].Question, "notes not verified") {
		t.Fatalf("an unchecked change was worded %q", ask.Questions[0].Question)
	}

	if status, result := f.call(t, "notes", map[string]any{"action": "ping", "sessionId": "to"}); status != http.StatusOK || string(result.Result) != `"pong"` {
		t.Fatalf("calling ping answered %d %+v", status, result)
	}
	ask, _ = openChangeAsk(t, reg, "to")
	if !strings.Contains(ask.Questions[0].Question, "notes verified: ping ran ok") {
		t.Fatalf("a successful call against the committed version left %q", ask.Questions[0].Question)
	}

	server := filepath.Join(workspace, "notes", "server.ts")
	if err := os.WriteFile(server, []byte(strings.Replace(tableServer, "'pong'", "'pong again'", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	f.turn(t, "to", "turn-2")
	ask, _ = openChangeAsk(t, reg, "to")
	if !strings.Contains(ask.Questions[0].Question, "notes not verified") {
		t.Fatalf("a later version kept the earlier call's check: %q", ask.Questions[0].Question)
	}
	if status, _ := f.call(t, "notes", map[string]any{"action": "missing"}); status != http.StatusNotFound {
		t.Fatalf("calling a missing action answered %d", status)
	}
	ask, _ = openChangeAsk(t, reg, "to")
	if !strings.Contains(ask.Questions[0].Question, "notes not verified") {
		t.Fatalf("a failed call checked the change: %q", ask.Questions[0].Question)
	}

	if err := os.WriteFile(server, []byte(strings.Replace(tableServer, "'pong'", "'edited after the turn'", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	status, result := f.call(t, "notes", map[string]any{"action": "ping"})
	if status != http.StatusOK || !result.Reloaded || string(result.Result) != `"edited after the turn"` {
		t.Fatalf("a call after an edit did not run the edit: %d %+v", status, result)
	}
	ask, _ = openChangeAsk(t, reg, "to")
	if !strings.Contains(ask.Questions[0].Question, "notes not verified") {
		t.Fatalf("a call against an unrecorded edit checked the recorded change: %q", ask.Questions[0].Question)
	}

	status, result = f.call(t, "fixture", map[string]any{"action": "caller", "sessionId": "to"})
	var caller extensionCallerInfo
	if status != http.StatusOK || json.Unmarshal(result.Result, &caller) != nil || caller.Kind != "session" || !strings.HasPrefix(caller.Actor, "session ") {
		t.Fatalf("a call from a session reached the extension as %d %s", status, result.Result)
	}
	status, result = f.call(t, "fixture", map[string]any{"action": "caller"})
	if status != http.StatusOK || json.Unmarshal(result.Result, &caller) != nil || caller.Kind != "person" {
		t.Fatalf("a call from the person reached the extension as %d %s", status, result.Result)
	}
}

func openGrantAsks(t *testing.T, reg *registry.Registry, sessionID string) []registry.Ask {
	t.Helper()
	asks, err := reg.SessionAsks(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(asks, func(ask registry.Ask) bool {
		return ask.Origin != registry.AskOriginExtensionGrant || ask.State != registry.AskOpen
	})
}

func TestApprovingFromASessionAsksThePersonAndGrantsOnlyWhatTheyWereShown(t *testing.T) {
	f := startExtensionFixture(t)
	reg := f.d.Registry
	workspace := extension.Workspace(f.home)
	writeExtension(t, workspace, "notes", `{"label": "Notes", "capabilities": ["read.all"]}`)
	f.turn(t, "to", "turn-1")

	approve := map[string]string{"action": "approve", "id": "notes", "sessionId": "to"}
	for range 2 {
		response, body := f.request(t, http.MethodPost, "/api/v1/extensions", "secret", approve)
		if response.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "ask_the_person") {
			t.Fatalf("approve from a session answered %d %s", response.StatusCode, body)
		}
	}
	asks := openGrantAsks(t, reg, "to")
	if len(asks) != 1 || !strings.Contains(asks[0].Questions[0].Question, "read.all") {
		t.Fatalf("approve from a session raised %+v", asks)
	}
	if grant, _ := f.d.extensions.approvals.Granted("notes"); len(grant.Capabilities) != 0 {
		t.Fatalf("the session's approve granted %+v before the person answered", grant)
	}
	session, err := reg.Session("to")
	if err != nil {
		t.Fatal(err)
	}
	actingForThePerson := sessionPrincipal("from")
	actingForThePerson.ForPerson = true
	for _, ask := range []registry.Ask{asks[0], mustOpenChangeAsk(t, reg, "to")} {
		if _, err := f.d.answerAsk(context.Background(), nil, session, ask.ID, surface.Response{Choices: []string{ask.Questions[0].Options[0].ID}}, actingForThePerson, "", ""); !strings.Contains(fmt.Sprint(err), "only the person") {
			t.Fatalf("a session acting for the person answered %s: %v", ask.Origin, err)
		}
	}
	if len(openGrantAsks(t, reg, "to")) != 1 {
		t.Fatal("a refused answer closed the grant ask")
	}
	if grant, _ := f.d.extensions.approvals.Granted("notes"); len(grant.Capabilities) != 0 {
		t.Fatalf("a session acting for the person granted %+v", grant)
	}
	if status := f.answer(t, asks[0], extensionGrantYes); status != http.StatusOK {
		t.Fatalf("the person's approval answered %d", status)
	}
	if grant, _ := f.d.extensions.approvals.Granted("notes"); !slices.Equal(grant.Capabilities, []string{"read.all"}) {
		t.Fatalf("the person's approval granted %+v", grant)
	}

	writeExtension(t, workspace, "notes", `{"label": "Notes", "capabilities": ["read.all", "control.all"]}`)
	if response, body := f.request(t, http.MethodPost, "/api/v1/extensions", "secret", approve); response.StatusCode != http.StatusForbidden {
		t.Fatalf("the second approve from a session answered %d %s", response.StatusCode, body)
	}
	asks = openGrantAsks(t, reg, "to")
	if len(asks) != 1 || !strings.Contains(asks[0].Questions[0].Question, "control.all") {
		t.Fatalf("the second approve raised %+v", asks)
	}
	writeExtension(t, workspace, "notes", `{"label": "Notes", "capabilities": ["read.all", "control.all", "requests.answer"]}`)
	if status := f.answer(t, asks[0], extensionGrantYes); status != http.StatusConflict {
		t.Fatalf("approving a manifest that changed after the ask answered %d", status)
	}
	if grant, _ := f.d.extensions.approvals.Granted("notes"); !slices.Equal(grant.Capabilities, []string{"read.all"}) {
		t.Fatalf("a changed manifest was granted: %+v", grant)
	}
	if status := f.answer(t, asks[0], extensionGrantNo); status != http.StatusOK || len(openGrantAsks(t, reg, "to")) != 0 {
		t.Fatalf("declining answered %d", status)
	}
}

func mustOpenChangeAsk(t *testing.T, reg *registry.Registry, sessionID string) registry.Ask {
	t.Helper()
	ask, open := openChangeAsk(t, reg, sessionID)
	if !open {
		t.Fatalf("%s has no open change ask", sessionID)
	}
	return ask
}

func TestARunThatAReloadOverlapsIsNotRecordedAsACheck(t *testing.T) {
	f := startExtensionFixture(t)
	type outcome struct {
		status int
		result extensionCallResult
	}
	done := make(chan outcome, 1)
	go func() {
		status, result := f.call(t, "fixture", map[string]any{"action": "held"})
		done <- outcome{status, result}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := poll.Until(ctx, 20*time.Millisecond, func() (bool, error) {
		_, err := os.Stat(filepath.Join(f.workdir, "held-started"))
		return err == nil, nil
	}); err != nil {
		t.Fatal("the held action never started")
	}
	before, _ := f.d.extensions.snapshot("fixture")
	if err := f.d.reloadExtensions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.workdir, "held-release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.status != http.StatusOK || string(got.result.Result) != `"released"` {
		t.Fatalf("the held action answered %d %+v", got.status, got.result)
	}
	if got.result.Tree != "" {
		t.Fatalf("a run a reload overlapped was recorded against %s", got.result.Tree)
	}
	if _, ok := f.d.extensions.calls.verified("fixture", before.tree); ok {
		t.Fatal("a run a reload overlapped checks the version loaded before it")
	}
}

func TestEachPendingChangeIsKeptOnItsOwnAndShowsWhatItChanged(t *testing.T) {
	f := startExtensionFixture(t)
	reg := f.d.Registry
	workspace := extension.Workspace(f.home)
	writeExtension(t, workspace, "notes", `{"label": "Notes"}`)
	f.turn(t, "to", "turn-1")
	writeExtension(t, workspace, "jots", `{"label": "Jots"}`)
	f.turn(t, "to", "turn-2")

	response, body := f.request(t, http.MethodGet, "/api/v1/extensions/history", "secret", nil)
	var listed struct {
		Changes []extensionChangeView `json:"changes"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &listed) != nil || len(listed.Changes) < 2 || !listed.Changes[0].Pending || !listed.Changes[1].Pending {
		t.Fatalf("history listed %d %s", response.StatusCode, body)
	}
	jots, notes := listed.Changes[0], listed.Changes[1]

	response, body = f.request(t, http.MethodGet, "/api/v1/extensions/history/"+notes.Rev, "secret", nil)
	var shown struct {
		Patch string `json:"patch"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &shown) != nil || !strings.Contains(shown.Patch, "+++ b/notes/extension.json") || strings.Contains(shown.Patch, "jots/") {
		t.Fatalf("the notes change showed %d %s", response.StatusCode, body)
	}

	if response, body := f.request(t, http.MethodPost, "/api/v1/extensions", "secret", map[string]string{"action": "keep", "rev": notes.Rev}); response.StatusCode != http.StatusOK {
		t.Fatalf("keeping one change answered %d %s", response.StatusCode, body)
	}
	ask := mustOpenChangeAsk(t, reg, "to")
	if strings.Contains(ask.Title, "turns") || !strings.Contains(ask.Questions[0].Question, "created jots") {
		t.Fatalf("after keeping notes the ask reads %q / %q", ask.Title, ask.Questions[0].Question)
	}
	if response, _ := f.request(t, http.MethodPost, "/api/v1/extensions", "secret", map[string]string{"action": "keep", "rev": notes.Rev}); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("keeping a kept change again answered %d", response.StatusCode)
	}
	if response, body := f.request(t, http.MethodPost, "/api/v1/extensions", "secret", map[string]string{"action": "keep", "rev": jots.Rev}); response.StatusCode != http.StatusOK {
		t.Fatalf("keeping the last change answered %d %s", response.StatusCode, body)
	}
	if _, open := openChangeAsk(t, reg, "to"); open {
		t.Fatal("the ask stayed open with every change kept")
	}
	if closed, found, _ := reg.Ask(ask.ID); !found || closed.Answer.Answers[closed.Questions[0].Question] != extensionKeep {
		t.Fatalf("the ask closed as %+v", closed.Answer)
	}
	if _, body := f.request(t, http.MethodGet, "/api/v1/extensions/history", "secret", nil); strings.Contains(string(body), `"pending":true`) {
		t.Fatalf("kept changes are still listed as pending: %s", body)
	}
}

func TestAStandalonePageHearsOnlyChangesAfterItLoadedAndOnlyItsOwn(t *testing.T) {
	f := startExtensionFixture(t)
	response, body := f.request(t, http.MethodGet, "/x/_ah.js", "secret", nil)
	cursor := f.d.events.cursor()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), fmt.Sprintf("const changesAfter = %d;\n", cursor)) {
		t.Fatalf("_ah.js began %.80q, want the cursor %d", body, cursor)
	}
	writeExtension(t, extension.Workspace(f.home), "notes", `{"label": "Notes"}`)
	f.turn(t, "to", "turn-1")
	backlog, follower, _ := f.d.events.subscribe(cursor)
	follower.Cancel()
	var changed []string
	for _, event := range backlog {
		if event.Type != "extension.changed" {
			continue
		}
		var data struct {
			Extensions []string `json:"extensions"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		changed = append(changed, data.Extensions...)
	}
	if !slices.Equal(changed, []string{"notes"}) {
		t.Fatalf("the change after the page loaded named %v", changed)
	}
}
