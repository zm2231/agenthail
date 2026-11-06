package daemon

import (
	"context"
	"slices"
	"testing"

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
