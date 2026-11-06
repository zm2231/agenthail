package daemon

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

type remoteMoveFixture struct {
	d     *Daemon
	r     *registry.Registry
	repo  string
	store *ledger.Store
	post  func(prompt, text string) ledgerCaptureResponse
}

func newRemoteMoveFixture(t *testing.T, added bool) remoteMoveFixture {
	t.Helper()
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	repo := catalogTestRepo(t)
	gitRun(t, repo, "remote", "add", "origin", "https://github.com/example/notes.git")
	session := surface.Session{ID: "worker", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: repo, Status: surface.StatusIdle}
	fake := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{session.ID: session}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	d := newTestDaemon(t, r, []surface.Surface{fake})
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if added {
		addFolderProject(t, d, repo)
	}
	handler := d.dashboardHandler(&dashboardServer{token: "test-token"})
	f := remoteMoveFixture{d: d, r: r, repo: repo, store: d.ledgerStore()}
	f.post = func(prompt, text string) ledgerCaptureResponse {
		return ledgerPost(t, handler, ledgerCaptureRequest{SessionID: session.ID, PromptID: prompt, Prompt: text})
	}
	d.discoverCatalog(context.Background())
	return f
}

func (f remoteMoveFixture) setRemote(t *testing.T, url string) {
	t.Helper()
	gitRun(t, f.repo, "remote", "set-url", "origin", url)
	f.d.discoverCatalog(context.Background())
}

func TestARemoteChangeCarriesTheProjectsLedger(t *testing.T) {
	for name, added := range map[string]bool{"implicit": false, "added": true} {
		t.Run(name, func(t *testing.T) {
			f := newRemoteMoveFixture(t, added)
			const before, after = "repo:github.com/example/notes", "repo:github.com/example/renamed-notes"
			if key := ledgerKeyAt(t, f.r, f.repo); key != before {
				t.Fatalf("ledger key = %q, want %s", key, before)
			}
			if err := f.store.Enable(before, ledger.AttachEachPrompt); err != nil {
				t.Fatal(err)
			}
			f.post("p1", "before the rename")

			f.setRemote(t, "https://github.com/example/renamed-notes.git")
			if key := ledgerKeyAt(t, f.r, f.repo); key != after {
				t.Fatalf("ledger key after the rename = %q, want %s", key, after)
			}
			if texts := utteranceTexts(t, f.store, after); !slices.Equal(texts, []string{"before the rename"}) {
				t.Fatalf("the renamed project's ledger holds %q, want its history", texts)
			}
			if held, err := f.r.HoldsLedger(before); err != nil || held {
				t.Fatalf("the old key still holds a ledger: %t %v", held, err)
			}
			if resp := f.post("p2", "after the rename"); resp.ProjectKey != after {
				t.Fatalf("a capture after the rename went to %q", resp.ProjectKey)
			}

			f.setRemote(t, "https://github.com/example/notes.git")
			if key := ledgerKeyAt(t, f.r, f.repo); key != before {
				t.Fatalf("ledger key after renaming back = %q, want %s", key, before)
			}
			if texts := utteranceTexts(t, f.store, before); !slices.Equal(texts, []string{"before the rename", "after the rename"}) {
				t.Fatalf("renaming back left the ledger holding %q", texts)
			}
		})
	}
}

func TestARemoteChangeCarriesTheProjectsLeadAndLinks(t *testing.T) {
	f := newRemoteMoveFixture(t, false)
	const before, after = "repo:github.com/example/notes", "repo:github.com/example/renamed-notes"
	joined := widgetRepo(t, "https://github.com/example/notes-docs.git")
	if _, err := f.r.SetCoordinator(registry.CoordinatorSpec{ProjectID: before, ProjectRoot: f.repo, Name: "notes-lead", Surface: "claude", Cwd: f.repo}, registry.CoordinatorChange{SessionID: "worker", ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.SetProjectLink("repo:github.com/example/notes-docs", before, true); err != nil {
		t.Fatal(err)
	}

	f.setRemote(t, "https://github.com/example/renamed-notes.git")
	if lead, err := f.r.Coordinator(after); err != nil || lead.Name != "notes-lead" {
		t.Fatalf("the lead did not move with the project: %+v %v", lead, err)
	}
	if project, err := ResolvePathProject(context.Background(), f.r, joined); err != nil || project.ID != after {
		t.Fatalf("the linked project resolves as %+v %v, want %s", project, err, after)
	}
	links, err := f.r.ProjectLinks()
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if link.Linked && (link.Survivor == before || link.Joined == before) {
			t.Fatalf("a link still names the old key: %+v", link)
		}
	}
}

func TestARemoteChangeOntoAnotherLedgerKeepsBothAndSaysSo(t *testing.T) {
	f := newRemoteMoveFixture(t, false)
	const before, taken = "repo:github.com/example/notes", "repo:github.com/example/other"
	for _, key := range []string{before, taken} {
		if err := f.store.Enable(key, ledger.AttachEachPrompt); err != nil {
			t.Fatal(err)
		}
	}
	f.post("p1", "notes history")

	f.setRemote(t, "https://github.com/example/other.git")
	if texts := utteranceTexts(t, f.store, before); !slices.Equal(texts, []string{"notes history"}) {
		t.Fatalf("the refused move lost the old ledger: %q", texts)
	}
	board, err := f.d.ledgerRead(taken, ledger.ReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	refusals, _ := board["notCarried"].([]registry.ProjectMoveRefusal)
	if len(refusals) != 1 || refusals[0].From != before || refusals[0].To != taken {
		t.Fatalf("the board does not say the history stayed behind: %+v", board["notCarried"])
	}

	f.setRemote(t, "https://github.com/example/notes.git")
	if refusals, err := f.r.ProjectMoveRefusals(); err != nil || len(refusals) != 0 {
		t.Fatalf("renaming back left the refusal: %+v %v", refusals, err)
	}
}

func TestAWriteThatResolvedTheOldKeyIsRefusedAfterTheMove(t *testing.T) {
	f := newRemoteMoveFixture(t, false)
	const before, after = "repo:github.com/example/notes", "repo:github.com/example/renamed-notes"
	if err := f.store.Enable(before, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	f.setRemote(t, "https://github.com/example/renamed-notes.git")

	_, _, err := f.store.RecordUtterance(ledger.Utterance{ProjectKey: before, Ref: "claude-prompt:late", Text: "resolved before the move", SpeakerID: "agenthail-person", TargetID: "worker", At: time.Now(), CapturedBy: "hook"})
	var refusal *ledger.Refusal
	if !errors.As(err, &refusal) || refusal.MovedTo != after {
		t.Fatalf("a write under the moved key = %v, want a refusal naming %s", err, after)
	}
	if err := f.store.GrantSession(before, "worker"); !errors.As(err, &refusal) {
		t.Fatalf("a grant under the moved key = %v, want a refusal", err)
	}
	if held, err := f.r.HoldsLedger(before); err != nil || held {
		t.Fatalf("the old key holds a ledger again: %t %v", held, err)
	}
}

func TestARemoteChangeLeavesNoLinkRecordOnTheOldKey(t *testing.T) {
	f := newRemoteMoveFixture(t, false)
	const before, after = "repo:github.com/example/notes", "repo:github.com/example/renamed-notes"
	const docs, upstream = "repo:github.com/example/notes-docs", "repo:github.com/example/upstream"
	for _, link := range [][2]string{{docs, before}, {before, upstream}} {
		if _, err := f.r.SetProjectLink(link[0], link[1], true); err != nil {
			t.Fatal(err)
		}
		if _, err := f.r.SetProjectLink(link[0], "", false); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.r.PutHostCoordinator("vps", registry.CoordinatorEntry{ProjectID: before, ProjectRoot: "/srv/notes", Name: "vps-notes", Surface: "codex", ChangedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := f.r.PutHostCoordinator("vps", registry.CoordinatorEntry{ProjectID: after, ProjectRoot: "/srv/renamed", Name: "vps-renamed", Surface: "codex", ChangedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	f.setRemote(t, "https://github.com/example/renamed-notes.git")
	if _, err := f.r.AdoptProjectLinks([]registry.ProjectLink{{Joined: before, Survivor: upstream, Linked: true, ChangedAt: time.Now().Add(-time.Hour), OriginHost: "stale"}}); err != nil {
		t.Fatal(err)
	}
	links, err := f.r.ProjectLinks()
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if link.Joined == before || link.Survivor == before {
			t.Fatalf("a link record still names the old key: %+v", link)
		}
	}
	entries, err := f.r.Coordinators()
	if err != nil {
		t.Fatal(err)
	}
	mirrored := map[string]string{}
	for _, entry := range entries {
		if entry.HostID == "vps" {
			mirrored[entry.ProjectID] = entry.Name
		}
	}
	if mirrored[before] != "vps-notes" || mirrored[after] != "vps-renamed" {
		t.Fatalf("the paired host's leads changed under it: %v", mirrored)
	}
}
