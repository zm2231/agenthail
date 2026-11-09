package surfaces

import (
	"context"
	"time"
)

// A second request, or one of several resolving, changes neither status nor file; only subscribers hear it.
type requestWatch struct {
	codex   *Codex
	client  *managedCodexClient
	want    map[string]bool
	have    map[string]bool
	busy    map[string]bool
	noted   map[string]bool
	results chan requestSubscription
	seeded  chan seededStatus
}

type requestSubscription struct {
	threadID   string
	subscribed bool
}

type seededStatus struct {
	threadID string
	status   any
}

func newRequestWatch(codex *Codex, client *managedCodexClient) *requestWatch {
	return &requestWatch{codex: codex, client: client, want: map[string]bool{}, have: map[string]bool{}, busy: map[string]bool{}, noted: map[string]bool{}, results: make(chan requestSubscription), seeded: make(chan seededStatus)}
}

// A thread already waiting at attach sends no status change.
func (w *requestWatch) seed(ctx context.Context, ids []string) {
	go func() {
		for _, id := range ids {
			response, err := w.client.Request(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false}, 5*time.Second)
			if err != nil {
				continue
			}
			result, _ := response["result"].(map[string]any)
			thread, _ := result["thread"].(map[string]any)
			select {
			case w.seeded <- seededStatus{threadID: id, status: thread["status"]}:
			case <-ctx.Done():
				return
			case <-w.client.Done():
				return
			}
		}
	}()
}

// A status change heard since attach is newer than the seed's read.
func (w *requestWatch) applySeed(ctx context.Context, seeded seededStatus) {
	if !w.noted[seeded.threadID] {
		w.apply(ctx, seeded.threadID, seeded.status)
	}
}

func (w *requestWatch) status(ctx context.Context, threadID string, status any) {
	if threadID == "" {
		return
	}
	w.noted[threadID] = true
	w.apply(ctx, threadID, status)
}

func (w *requestWatch) apply(ctx context.Context, threadID string, status any) {
	w.want[threadID] = codexWaitingKind(status) != ""
	if !w.want[threadID] {
		delete(w.want, threadID)
	}
	w.reconcile(ctx, threadID)
}

func (w *requestWatch) reconcile(ctx context.Context, threadID string) {
	if w.busy[threadID] || w.want[threadID] == w.have[threadID] {
		return
	}
	w.busy[threadID] = true
	subscribe := w.want[threadID]
	go func() {
		result := requestSubscription{threadID: threadID}
		if subscribe {
			result.subscribed = w.codex.subscribeManagedThread(ctx, w.client, threadID)
		} else {
			release := w.codex.subscriptions.shared(threadID)
			w.codex.subscriptions.remove(threadID, w.client)
			_, _ = w.client.Request(ctx, "thread/unsubscribe", map[string]any{"threadId": threadID}, 5*time.Second)
			release()
		}
		select {
		case w.results <- result:
		case <-ctx.Done():
		case <-w.client.Done():
		}
	}()
}

// finish reports a new subscription, since requests may have opened or
// resolved before it took. A thread no longer loaded is not subscribed; its
// next status change tries again.
func (w *requestWatch) finish(ctx context.Context, result requestSubscription) bool {
	delete(w.busy, result.threadID)
	subscribed := result.subscribed && !w.have[result.threadID]
	if result.subscribed {
		w.have[result.threadID] = true
	} else {
		delete(w.have, result.threadID)
		if w.want[result.threadID] {
			delete(w.want, result.threadID)
			return false
		}
	}
	w.reconcile(ctx, result.threadID)
	return subscribed
}

// A thread being subscribed counts too: the resume's replies can arrive before its result.
func (w *requestWatch) heard(event codexEvent) bool {
	if len(event.RequestID) == 0 && event.Method != codexRequestResolved {
		return false
	}
	threadID, _ := event.Params["threadId"].(string)
	return w.have[threadID] || w.want[threadID]
}
