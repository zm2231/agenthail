package daemon

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestTheLeadFinishesAMergedItemWhoseHolderIsArchivedButTheDaemonNotTheCallerJudgesTheHolder(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	path := plainFolder(t)
	projectID := addFolderProject(t, d, path)
	key := ledgerKeyAt(t, r, path)
	store := d.ledgerStore()
	if err := store.Enable(key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	worker := folderSession(t, r, "worker", path)
	lead := folderSession(t, r, "lead", path)
	peer := folderSession(t, r, "peer", path)
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: projectID, ProjectRoot: path, Name: "widget-lead", Surface: string(surface.KindClaude), Cwd: path}, registry.CoordinatorChange{SessionID: lead.ID, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var told []string
	d.notifyDesktop = func(_, message, _, _, _ string, _ []surface.RequestAction) error {
		mu.Lock()
		defer mu.Unlock()
		told = append(told, message)
		return nil
	}
	work := func(req ledger.WorkRequest) ledger.WorkResult {
		t.Helper()
		req.ProjectKey = key
		result, err := store.Work(req, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	merged := work(ledger.WorkRequest{SessionID: worker.ID, Action: ledger.WorkAdd, Text: "fix the login"}).Item.ID
	work(ledger.WorkRequest{SessionID: worker.ID, Action: ledger.WorkClaim, ItemID: merged})
	vanished := work(ledger.WorkRequest{SessionID: worker.ID, Action: ledger.WorkAdd, Text: "add the test"}).Item.ID
	work(ledger.WorkRequest{SessionID: "vanished", Action: ledger.WorkClaim, ItemID: vanished})

	ref := github.Ref{Repo: acmeApp, Number: 42}
	fake := &fakeGitHub{prs: map[github.Ref]github.PullRequest{ref: acmePR(42, "Fix the login")}}
	d.github = fake
	server := mcpServer(t, d)
	if out, refused := callMCP(t, mcpSession(t, server, issueMCPToken(t, d, worker)), "agenthail_pr", map[string]any{"action": "link", "pr": ref.URL(), "clientRequestId": "link"}); refused {
		t.Fatalf("link: %v", out)
	}
	status := openStatus(ref, "abc123")
	status.State = "merged"
	fake.setStatus(status)
	if err := d.watchPRs(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}

	calls := 0
	clients := map[string]*mcp.ClientSession{}
	takeover := func(caller surface.Session, item, then string) (map[string]any, bool) {
		t.Helper()
		session, open := clients[caller.ID]
		if !open {
			session = mcpSession(t, server, issueMCPToken(t, d, caller))
			clients[caller.ID] = session
		}
		calls++
		n := calls
		request := map[string]any{"action": "takeover", "itemId": item, "reason": "its PR merged and the worker is done", "takeover": map[string]any{"then": then, "holder": "worker", "verdict": "gone"}}
		return callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": fmt.Sprintf("takeover-%d", n), "request": request})
	}

	if out, refused := takeover(lead, merged, ledger.TakeoverDone); !refused || !strings.Contains(fmt.Sprint(out["message"]), "is still running") {
		t.Fatalf("taking over from a running holder that the request calls gone = %v", out)
	}
	if err := r.ArchiveSession(worker.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if out, refused := takeover(peer, merged, ledger.TakeoverDone); !refused || !strings.Contains(fmt.Sprint(out["message"]), "for the person or your project's coordinator") {
		t.Fatalf("a worker taking over = %v", out)
	}
	out, refused := takeover(lead, merged, ledger.TakeoverDone)
	item, _ := out["item"].(map[string]any)
	if refused || item["state"] != string(ledger.WorkDone) {
		t.Fatalf("the lead finishing a merged item of an archived holder = %v", out)
	}
	board, err := store.Board(key)
	if err != nil {
		t.Fatal(err)
	}
	done, _ := board.WorkItem(merged)
	last := done.Timeline[len(done.Timeline)-1]
	if last.Actor != lead.ID || !strings.Contains(last.Text, "taken over from worker (archived)") || !strings.Contains(last.Text, "PR merged: "+ref.URL()) {
		t.Fatalf("the takeover's log line = %+v", last)
	}
	mu.Lock()
	notified := strings.Join(told, "\n")
	mu.Unlock()
	if !strings.Contains(notified, "Took over work item "+merged) {
		t.Fatalf("the person was told %q", notified)
	}

	if out, refused := takeover(lead, vanished, ledger.TakeoverDone); !refused || !strings.Contains(fmt.Sprint(out["message"]), "PR watcher") {
		t.Fatalf("finishing an item with no merged PR = %v", out)
	}
	out, refused = takeover(lead, vanished, ledger.TakeoverRelease)
	item, _ = out["item"].(map[string]any)
	if refused || item["state"] != string(ledger.WorkOpen) {
		t.Fatalf("releasing the item of a holder no host knows = %v", out)
	}
	board, _ = store.Board(key)
	released, _ := board.WorkItem(vanished)
	if text := released.Timeline[len(released.Timeline)-1].Text; !strings.Contains(text, "taken over from vanished (gone)") {
		t.Fatalf("the release's log line = %q", text)
	}
}
