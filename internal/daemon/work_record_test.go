package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/worktree"
	"github.com/zm2231/agenthail/internal/worktree/worktreetest"
)

var (
	gitIn          = worktreetest.Git
	pushedCheckout = worktreetest.PushedCheckout
)

func workAction(t *testing.T, d *Daemon, body map[string]any) (int, map[string]any) {
	t.Helper()
	response := postAction(t, dashboardRouter(d), body)
	var answer map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &answer)
	return response.Code, answer
}

func settleIdle(t *testing.T, reg *registry.Registry, id string) {
	t.Helper()
	session, err := reg.Session(id)
	if err != nil {
		t.Fatal(err)
	}
	session.Status = surface.StatusIdle
	if err := reg.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
}

func workOf(t *testing.T, reg *registry.Registry, id string) registry.WorkRecord {
	t.Helper()
	work, found, err := reg.WorkForSession(id)
	if err != nil || !found {
		t.Fatalf("work for %s: found=%t err=%v", id, found, err)
	}
	return work
}

func TestASessionStartedInItsOwnWorktreeKeepsUnpushedWorkAndIsRemovedOncePushed(t *testing.T) {
	checkout := pushedCheckout(t)
	d, reg, fake, _, _ := daemonFixture(t)
	ceiling := sessionCeiling{Permission: "edits", Roots: []string{checkout}}
	code, answer := workAction(t, d, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": filepath.Join(checkout, "app"), "isolation": "worktree", "alias": "builder", "ceiling": ceiling.encode()})
	if code != http.StatusCreated {
		t.Fatal(code, answer)
	}
	path := filepath.Join(checkout, worktree.Dir, "ah-builder")
	if made, _ := answer["worktree"].(map[string]any); made["path"] != path || made["branch"] != "ah/builder" || made["base"] != "main" {
		t.Fatalf("worktree answer %v", answer["worktree"])
	}
	if got := fake.startOptions[0].Cwd; got != filepath.Join(path, "app") {
		t.Fatalf("started in %s", got)
	}
	var child sessionCeiling
	if err := json.Unmarshal(fake.startCeilings[0], &child); err != nil || len(child.Roots) != 1 || child.Roots[0] != path {
		t.Fatalf("child ceiling %s err=%v", fake.startCeilings[0], err)
	}
	if status := gitIn(t, checkout, "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("the main checkout shows the worktree: %q", status)
	}
	if work := workOf(t, reg, "started"); work.State != registry.WorkActive || work.WorktreePath != path {
		t.Fatalf("work %+v", work)
	}

	if err := os.WriteFile(filepath.Join(path, "app", "feature.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, path, "add", ".")
	gitIn(t, path, "commit", "--quiet", "-m", "feature")
	if code, answer := workAction(t, d, map[string]any{"action": "session-archive", "sessionId": "started"}); code != http.StatusOK {
		t.Fatal(code, answer)
	}
	d.sweepWork(context.Background(), time.Now())
	if work := workOf(t, reg, "started"); work.State != registry.WorkSettled || work.SettledReason != registry.WorkSettledArchived || work.Cleanup != registry.WorkCleanupKept || work.CleanupReason != registry.WorkKeptRunning {
		t.Fatalf("a session mid-turn lost its worktree: %+v", work)
	}
	settleIdle(t, reg, "started")
	d.sweepWork(context.Background(), time.Now())
	if work := workOf(t, reg, "started"); work.Cleanup != registry.WorkCleanupKept || work.CleanupReason != worktree.KeptUnpushed {
		t.Fatalf("unpushed work %+v", work)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unpushed worktree removed: %v", err)
	}

	gitIn(t, path, "push", "--quiet", "origin", "ah/builder")
	code, answer = workAction(t, d, map[string]any{"action": "work-clean", "sessionId": "started"})
	if code != http.StatusOK {
		t.Fatal(code, answer)
	}
	if cleaned, _ := answer["worktree"].(map[string]any); cleaned["cleanup"] != registry.WorkCleanupRemoved {
		t.Fatalf("clean answer %v", answer)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree still there: %v", err)
	}
	if branches := gitIn(t, checkout, "branch", "--list", "ah/builder"); branches != "" {
		t.Fatalf("branch kept: %q", branches)
	}
}

func TestAPersonCleaningASessionsWorktreeKeepsUncommittedChanges(t *testing.T) {
	checkout := pushedCheckout(t)
	d, reg, _, _, _ := daemonFixture(t)
	if code, answer := workAction(t, d, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": checkout, "worktree": "fix/typo"}); code != http.StatusCreated {
		t.Fatal(code, answer)
	}
	settleIdle(t, reg, "started")
	path := filepath.Join(checkout, worktree.Dir, "fix-typo")
	if err := os.WriteFile(filepath.Join(path, "notes.txt"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, answer := workAction(t, d, map[string]any{"action": "work-clean", "sessionId": "started"})
	if cleaned, _ := answer["worktree"].(map[string]any); code != http.StatusOK || cleaned["cleanup"] != registry.WorkCleanupKept || cleaned["cleanupReason"] != worktree.KeptDirty {
		t.Fatal(code, answer)
	}
	if work := workOf(t, reg, "started"); work.SettledReason != registry.WorkSettledPerson {
		t.Fatalf("work %+v", work)
	}
	if err := os.Remove(filepath.Join(path, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, ".git", "info", "exclude"), []byte("/.worktrees/\nlocal.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "local.env"), []byte("TOKEN=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, answer = workAction(t, d, map[string]any{"action": "work-clean", "sessionId": "started"})
	if cleaned, _ := answer["worktree"].(map[string]any); code != http.StatusOK || cleaned["cleanupReason"] != worktree.KeptIgnored || fmt.Sprint(cleaned["keptPaths"]) != "[local.env]" {
		t.Fatal(code, answer)
	}
	if work := workOf(t, reg, "started"); len(work.KeptPaths) != 1 || work.KeptPaths[0] != "local.env" {
		t.Fatalf("kept paths not recorded: %+v", work)
	}
	if code, answer := workAction(t, d, map[string]any{"action": "work-clean", "sessionId": "started", "forceIgnored": true}); code != http.StatusOK || answer["worktree"].(map[string]any)["cleanup"] != registry.WorkCleanupRemoved {
		t.Fatal(code, answer)
	}
}

func TestAWorktreeWhoseSessionNeverStartedIsTakenBack(t *testing.T) {
	checkout := pushedCheckout(t)
	d, reg, fake, _, _ := daemonFixture(t)
	fake.startRefused = errors.New("no app server")
	code, answer := workAction(t, d, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": checkout, "isolation": "worktree", "alias": "builder"})
	if code < 400 {
		t.Fatal(code, answer)
	}
	if _, err := os.Stat(filepath.Join(checkout, worktree.Dir, "ah-builder")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree left behind: %v", err)
	}
	if branches := gitIn(t, checkout, "branch", "--list", "ah/builder"); branches != "" {
		t.Fatalf("branch left behind: %q", branches)
	}
	if stale, err := reg.StaleCreatingWork(time.Now().Add(time.Hour)); err != nil || len(stale) != 0 {
		t.Fatalf("records left behind: %+v err=%v", stale, err)
	}
	if code, answer := workAction(t, d, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": t.TempDir(), "isolation": "worktree"}); code != http.StatusBadRequest {
		t.Fatalf("a folder outside git: %d %v", code, answer)
	}
}

func TestAWorktreeWhoseStartHasAnUnknownOutcomeIsKeptForTheSessionThatTurnsUp(t *testing.T) {
	checkout := pushedCheckout(t)
	d, reg, fake, _, _ := daemonFixture(t)
	fake.startRefused = surface.DeliveryOutcomeUnknown(errors.New("the app server stopped answering"))
	code, answer := workAction(t, d, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": checkout, "isolation": "worktree", "alias": "builder"})
	if code < 400 {
		t.Fatal(code, answer)
	}
	path := filepath.Join(checkout, worktree.Dir, "ah-builder")
	if err := os.WriteFile(filepath.Join(path, "progress.txt"), []byte("half done"), 0o600); err != nil {
		t.Fatalf("the worktree a session may be running in was removed: %v", err)
	}
	if err := reg.RegisterSession(surface.Session{ID: "late", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Cwd: path, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	d.sweepWork(context.Background(), time.Now().Add(2*workCreatingStale))
	if work := workOf(t, reg, "late"); work.State != registry.WorkActive || work.WorktreePath != path {
		t.Fatalf("the session that turned up does not own its worktree: %+v", work)
	}
	if _, err := os.Stat(filepath.Join(path, "progress.txt")); err != nil {
		t.Fatalf("the session's work was lost: %v", err)
	}
}

func TestAWorktreeWhoseStartHasAnUnknownOutcomeKeepsItsWorkWhenNoSessionTurnsUp(t *testing.T) {
	checkout := pushedCheckout(t)
	d, reg, fake, _, _ := daemonFixture(t)
	fake.startRefused = surface.DeliveryOutcomeUnknown(errors.New("the app server stopped answering"))
	if code, answer := workAction(t, d, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": checkout, "isolation": "worktree", "alias": "builder"}); code < 400 {
		t.Fatal(code, answer)
	}
	path := filepath.Join(checkout, worktree.Dir, "ah-builder")
	if err := os.WriteFile(filepath.Join(path, "progress.txt"), []byte("half done"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.sweepWork(context.Background(), time.Now().Add(2*workCreatingStale))
	if _, err := os.Stat(filepath.Join(path, "progress.txt")); err != nil {
		t.Fatalf("uncommitted work in the worktree was lost: %v", err)
	}
	if work, found, err := reg.WorkAt(path); err != nil || !found || work.State != registry.WorkSettled || work.Cleanup == registry.WorkCleanupRemoved {
		t.Fatalf("the kept worktree is not recorded as kept: %+v found=%t err=%v", work, found, err)
	}
}

func TestCleaningAnotherHostsSessionsWorktreeRunsOnThatHost(t *testing.T) {
	local, remote, record := pairedFederation(t)
	checkout := pushedCheckout(t)
	plan, err := worktree.Begin(context.Background(), remote.reg, worktree.Spec{Cwd: checkout, Name: "remote"})
	if err != nil {
		t.Fatal(err)
	}
	if activated, err := remote.reg.ActivateWork(plan.WorktreePath, "remote-a"); err != nil || !activated {
		t.Fatalf("activated=%t err=%v", activated, err)
	}
	response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "work-clean", "sessionId": hosts.Qualify(record.ID, "remote-a")})
	var answer struct {
		Worktree workView `json:"worktree"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil || answer.Worktree.Path != plan.WorktreePath || answer.Worktree.Cleanup != registry.WorkCleanupRemoved {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(plan.WorktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree left on its host: %v", err)
	}
}

func TestAWorktreeTakenOrSettledRepublishesItsSessionsRow(t *testing.T) {
	d, reg, _, _, _ := daemonFixture(t)
	path := filepath.Join(t.TempDir(), "wt")
	if err := reg.BeginWork(registry.WorkRecord{WorktreePath: path, RepoRoot: filepath.Dir(path), Branch: "ah/wt", BaseRef: "main", BaseCommit: "base"}, time.Now().Add(-2*workCreatingStale)); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterSession(surface.Session{ID: "worker", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Cwd: path, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	republished := func(step string) {
		t.Helper()
		select {
		case <-d.catalogNudge:
		default:
			t.Fatalf("%s did not republish the session's row", step)
		}
	}
	drain := func() {
		select {
		case <-d.catalogNudge:
		default:
		}
	}
	drain()
	d.sweepWork(context.Background(), time.Now())
	if work := workOf(t, reg, "worker"); work.State != registry.WorkActive {
		t.Fatalf("work %+v", work)
	}
	republished("handing the unfinished worktree to its session")
	d.settleWork("worker", registry.WorkSettledTaskReported)
	if work := workOf(t, reg, "worker"); work.State != registry.WorkSettled {
		t.Fatalf("work %+v", work)
	}
	republished("settling the worktree")
}

func TestAWorktreeRecordedOutsideTheDaemonReachesStreamsOnTheNextDiscoveryPass(t *testing.T) {
	d, reg, _, _, _ := daemonFixture(t)
	d.discoverCatalog(context.Background())
	_, before, err := reg.CatalogState()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "wt")
	if err := reg.BeginWork(registry.WorkRecord{WorktreePath: path, RepoRoot: filepath.Dir(path), Branch: "ah/wt", BaseRef: "main", BaseCommit: "base"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if activated, err := reg.ActivateWork(path, "from"); err != nil || !activated {
		t.Fatalf("activate %t %v", activated, err)
	}
	d.discoverCatalog(context.Background())
	window, err := reg.CatalogEventsAfter(before, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range window.Events {
		var payload struct {
			Session dashboardSession `json:"session"`
		}
		if event.Type == "session.upserted" && json.Unmarshal(event.Payload, &payload) == nil && payload.Session.ID == "from" && payload.Session.Worktree != nil && payload.Session.Worktree.Path == path {
			return
		}
	}
	t.Fatalf("no catalog event carried the worktree: %d events", len(window.Events))
}

func TestACreateThatCrashedIsReconciledFromItsRecord(t *testing.T) {
	checkout := pushedCheckout(t)
	d, reg, _, _, _ := daemonFixture(t)
	long := time.Now().Add(-2 * workCreatingStale)
	begin := func(name string) worktree.Plan {
		plan, err := worktree.NewPlan(context.Background(), worktree.Spec{Cwd: checkout, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		if err := reg.BeginWork(registry.WorkRecord{WorktreePath: plan.WorktreePath, RepoRoot: plan.RepoRoot, Branch: plan.Branch, BaseRef: plan.BaseRef, BaseCommit: plan.BaseCommit}, long); err != nil {
			t.Fatal(err)
		}
		if plan, err = worktree.Add(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
		return plan
	}
	adopted, orphan := begin("adopted"), begin("orphan")
	if err := reg.RegisterSession(surface.Session{ID: "survivor", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Cwd: adopted.Cwd, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	unmade, err := worktree.NewPlan(context.Background(), worktree.Spec{Cwd: checkout, Name: "unmade"})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.BeginWork(registry.WorkRecord{WorktreePath: unmade.WorktreePath, RepoRoot: unmade.RepoRoot, Branch: unmade.Branch, BaseRef: unmade.BaseRef, BaseCommit: unmade.BaseCommit}, long); err != nil {
		t.Fatal(err)
	}
	d.sweepWork(context.Background(), time.Now())
	if work := workOf(t, reg, "survivor"); work.State != registry.WorkActive || work.WorktreePath != adopted.WorktreePath {
		t.Fatalf("the started session lost its worktree: %+v", work)
	}
	for _, plan := range []worktree.Plan{orphan, unmade} {
		if _, found, err := reg.WorkAt(plan.WorktreePath); err != nil || found {
			t.Fatalf("record for %s found=%t err=%v", plan.WorktreePath, found, err)
		}
		if _, err := os.Stat(plan.WorktreePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("worktree %s left: %v", plan.WorktreePath, err)
		}
	}
	if branches := gitIn(t, checkout, "branch", "--list", "ah/orphan"); branches != "" {
		t.Fatalf("orphan branch left: %q", branches)
	}
}

func TestACreateAtAWorktreeAlreadyRecordedIsRefused(t *testing.T) {
	checkout := pushedCheckout(t)
	d, reg, fake, _, _ := daemonFixture(t)
	plan, err := worktree.NewPlan(context.Background(), worktree.Spec{Cwd: checkout, Branch: "ah/taken"})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.BeginWork(registry.WorkRecord{WorktreePath: plan.WorktreePath, RepoRoot: plan.RepoRoot, Branch: plan.Branch, BaseRef: plan.BaseRef, BaseCommit: plan.BaseCommit}, time.Now()); err != nil {
		t.Fatal(err)
	}
	code, answer := workAction(t, d, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": checkout, "worktree": "ah/taken"})
	if failure, _ := answer["error"].(map[string]any); code != http.StatusBadRequest || failure["code"] != "invalid_request" {
		t.Fatalf("a create racing another at the same worktree: %d %v", code, answer)
	}
	if len(fake.startOptions) != 0 {
		t.Fatalf("a refused create started a session: %+v", fake.startOptions)
	}
	if work, found, err := reg.WorkAt(plan.WorktreePath); err != nil || !found || work.State != registry.WorkCreating {
		t.Fatalf("the other create's record: %+v found=%t err=%v", work, found, err)
	}
}

func TestAWorktreeAnotherSessionIsWorkingInIsKeptUntilItStops(t *testing.T) {
	checkout := pushedCheckout(t)
	d, reg, _, _, _ := daemonFixture(t)
	if code, answer := workAction(t, d, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": checkout, "worktree": "ah/shared"}); code != http.StatusCreated {
		t.Fatal(code, answer)
	}
	settleIdle(t, reg, "started")
	path := filepath.Join(checkout, worktree.Dir, "ah-shared")
	if err := reg.RegisterSession(surface.Session{ID: "visitor", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Cwd: path, Status: surface.StatusBusy}); err != nil {
		t.Fatal(err)
	}
	if code, answer := workAction(t, d, map[string]any{"action": "session-archive", "sessionId": "started"}); code != http.StatusOK {
		t.Fatal(code, answer)
	}
	d.sweepWork(context.Background(), time.Now())
	if work := workOf(t, reg, "started"); work.Cleanup != registry.WorkCleanupKept || work.CleanupReason != registry.WorkKeptRunning {
		t.Fatalf("a worktree another session is mid-turn in was cleaned: %+v", work)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the worktree went while a session worked in it: %v", err)
	}
	settleIdle(t, reg, "visitor")
	d.sweepWork(context.Background(), time.Now())
	if work := workOf(t, reg, "started"); work.Cleanup != registry.WorkCleanupRemoved {
		t.Fatalf("the worktree was not cleaned once nothing worked in it: %+v", work)
	}
}

func TestARecordedWorktreeOutsideTheWorktreesFolderIsNeverRemoved(t *testing.T) {
	checkout := pushedCheckout(t)
	d, reg, _, _, _ := daemonFixture(t)
	resolved, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(filepath.Dir(resolved), "elsewhere")
	gitIn(t, checkout, "worktree", "add", "--quiet", "-b", "ah/elsewhere", elsewhere, "main")
	base := gitIn(t, checkout, "rev-parse", "main")
	if err := reg.BeginWork(registry.WorkRecord{WorktreePath: elsewhere, RepoRoot: resolved, Branch: "ah/elsewhere", BaseRef: "main", BaseCommit: base}, time.Now().Add(-2*workCreatingStale)); err != nil {
		t.Fatal(err)
	}
	d.sweepWork(context.Background(), time.Now())
	work, found, err := reg.WorkAt(elsewhere)
	if err != nil || !found || work.Cleanup != registry.WorkCleanupKept {
		t.Fatalf("a worktree Agenthail did not make was cleaned: %+v found=%t err=%v", work, found, err)
	}
	if _, err := os.Stat(elsewhere); err != nil {
		t.Fatalf("a worktree outside .worktrees was removed: %v", err)
	}
}
