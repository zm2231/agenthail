package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/sse"
	"github.com/zm2231/agenthail/internal/surface"
)

func ledgerGet(t *testing.T, handler http.Handler, target, ifNoneMatch string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Authorization", "Bearer test-token")
	if ifNoneMatch != "" {
		request.Header.Set("If-None-Match", ifNoneMatch)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	return w
}

func ledgerGetJSON(t *testing.T, handler http.Handler, target string) (map[string]any, string) {
	t.Helper()
	w := ledgerGet(t, handler, target, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", target, w.Code, w.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body, w.Header().Get("ETag")
}

func addWork(t *testing.T, store *ledger.Store, key, text string) ledger.WorkItem {
	t.Helper()
	result, err := store.Work(ledger.WorkRequest{ProjectKey: key, SessionID: "worker", Action: ledger.WorkAdd, Text: text}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return result.Item
}

func TestTheLedgerReadRouteServesEachViewWithAnETagOfItsContent(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-read", "read-key")
	p.session(t, r, "worker")
	store := d.ledgerStore()
	if err := store.Enable(p.key, ledger.AttachAtCheckpoints); err != nil {
		t.Fatal(err)
	}
	for _, words := range []string{"one", "two", "three", "four", "five", "six"} {
		if _, _, err := store.RecordUtterance(ledger.Utterance{ProjectKey: p.key, Ref: "claude-prompt:" + words, SpeakerID: "person", TargetID: "worker", Text: "say " + words, CapturedBy: ledger.CapturedByHook}); err != nil {
			t.Fatal(err)
		}
	}
	index := addWork(t, store, p.key, "build the index")
	board, etag := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key)
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) || len(etag) < 10 {
		t.Fatalf("etag %q is not a strong quoted tag", etag)
	}
	capture, _ := board["capture"].(map[string]any)
	unattached, _ := board["unattached"].(map[string]any)
	newest, _ := unattached["newest"].([]any)
	counts, _ := board["counts"].(map[string]any)
	if capture["state"] != "enabled" || capture["attach"] != "checkpoints" || unattached["total"] != 6.0 || len(newest) != 5 || counts["work"] != 1.0 {
		t.Fatalf("board %v", board)
	}
	if first, _ := newest[0].(map[string]any); first["text"] != "say two" {
		t.Fatalf("the board's unattached words are not the newest five: %v", newest)
	}

	if w := ledgerGet(t, handler, "/api/v1/ledger?projectKey="+p.key, etag); w.Code != http.StatusNotModified || w.Body.Len() != 0 || w.Header().Get("ETag") != etag {
		t.Fatalf("a matching If-None-Match answered %d with %q", w.Code, w.Body)
	}
	addWork(t, store, p.key, "keep it fast")
	if w := ledgerGet(t, handler, "/api/v1/ledger?projectKey="+p.key, etag); w.Code != http.StatusOK || w.Header().Get("ETag") == etag {
		t.Fatalf("a change kept the old ETag: %d %s", w.Code, w.Header().Get("ETag"))
	}

	shipped := addWork(t, store, p.key, "ship the old search")
	if _, err := store.Work(ledger.WorkRequest{ProjectKey: p.key, SessionID: "worker", Action: ledger.WorkClose, ItemID: shipped.ID, Reason: "done shipped last week"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	every, _ := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key+"&view=work")
	open, _ := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key+"&view=work&state=open")
	unsettled, _ := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key+"&view=work&all=false")
	if len(every["work"].([]any)) != 3 || len(open["work"].([]any)) != 2 || len(unsettled["work"].([]any)) != 2 {
		t.Fatalf("unfiltered work %v, open work %v, all=false %v", every["work"], open["work"], unsettled["work"])
	}
	work, _ := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key+"&view=work&q=index")
	if items, _ := work["work"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != index.ID {
		t.Fatalf("filtered work %v", work)
	}
	page, _ := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key+"&view=work&state=open&limit=1")
	next, _ := page["nextCursor"].(string)
	rest, _ := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key+"&view=work&state=open&limit=1&cursor="+next)
	if next != index.ID || rest["truncated"] != false || rest["work"].([]any)[0].(map[string]any)["text"] != "keep it fast" {
		t.Fatalf("pages %v then %v", page, rest)
	}
	item, _ := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key+"&view=item&id="+index.ID)
	current, _ := store.Board(p.key)
	context, _ := current.WorkContext(index.ID)
	want, _ := json.Marshal(context)
	if got, _ := json.Marshal(item["item"]); item["kind"] != "work" || string(got) != string(normalizedJSON(t, want)) {
		t.Fatalf("item view %s, want what show --json prints %s", got, want)
	}

	for target, status := range map[string]int{
		"/api/v1/ledger?projectKey=" + p.key + "&view=whatever":           http.StatusBadRequest,
		"/api/v1/ledger?projectKey=" + p.key + "&view=work&sort=priority": http.StatusBadRequest,
		"/api/v1/ledger?projectKey=" + p.key + "&view=item&id=read-W99":   http.StatusNotFound,
		"/api/v1/ledger?projectKey=no-such-key":                           http.StatusNotFound,
	} {
		if w := ledgerGet(t, handler, target, ""); w.Code != status {
			t.Errorf("GET %s answered %d, want %d", target, w.Code, status)
		}
	}
}

func normalizedJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(value)
	return out
}

func TestTheLedgerETagIsTheSameAfterARebuildAndChangesWithTheContent(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-etag", "etag-key")
	addWork(t, d.ledgerStore(), p.key, "build the index")
	_, before := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key)

	rebuilt := newTestDaemon(t, r, []surface.Surface{&daemonSurface{sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}})
	fresh := rebuilt.dashboardHandler(&dashboardServer{token: "test-token"})
	if _, after := ledgerGetJSON(t, fresh, "/api/v1/ledger?projectKey="+p.key); after != before {
		t.Fatalf("a board rebuilt from the log has ETag %s, the cached one %s", after, before)
	}
	addWork(t, rebuilt.ledgerStore(), p.key, "keep it fast")
	_, changed := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key)
	_, changedFresh := ledgerGetJSON(t, fresh, "/api/v1/ledger?projectKey="+p.key)
	if changed == before || changed != changedFresh {
		t.Fatalf("after a write: cached %s, rebuilt %s, before %s", changed, changedFresh, before)
	}
}

// shortLedgerChangeWait shortens the stream's coalescing window for one test;
// call it before the daemon first opens its ledger.
func shortLedgerChangeWait(t *testing.T) {
	wait := ledgerChangeWait
	ledgerChangeWait = 100 * time.Millisecond
	t.Cleanup(func() { ledgerChangeWait = wait })
}

func TestTheLedgerStreamSendsAFewVersionsForAReplayOfThousandsOfEvents(t *testing.T) {
	shortLedgerChangeWait(t)
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-stream", "stream-key")
	store := d.ledgerStore()
	addWork(t, store, p.key, "build the index")
	receive := openLedgerStream(t, handler)
	_, etag := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key)
	if first, ok := receive(5 * time.Second); !ok || first.ProjectKey != p.key || first.Version != etag {
		t.Fatalf("on connect the stream sent %+v, want %s at %s", first, p.key, etag)
	}

	const replayed = 3000
	began := time.Now()
	for i := range replayed {
		ref := "claude-prompt:replay-" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + time.Duration(i).String()
		if _, _, err := store.RecordUtterance(ledger.Utterance{ProjectKey: p.key, Ref: ref, SpeakerID: "person", TargetID: "lead", Text: "word", CapturedBy: ledger.CapturedByHook}); err != nil {
			t.Fatal(err)
		}
	}
	took := time.Since(began)
	var published []ledgerChange
	for {
		change, ok := receive(2 * ledgerChangeWait)
		if !ok {
			break
		}
		published = append(published, change)
	}
	if most := int(took/ledgerChangeWait) + 2; len(published) == 0 || len(published) > most {
		t.Fatalf("%d events written in %s gave %d publishes, want 1 to %d", replayed, took, len(published), most)
	}
	_, now := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key)
	if last := published[len(published)-1]; last.Version != now {
		t.Fatalf("the last version %s is not the board's ETag %s", last.Version, now)
	}
	addWork(t, store, p.key, "keep it fast")
	if change, ok := receive(5 * time.Second); !ok || change.ProjectKey != p.key {
		t.Fatal("the stream stopped after the replay")
	}
}

func TestAReplicaReadsItsOwnProxiedWriteAndShowsAPullThatFailed(t *testing.T) {
	mac, owner, record := ledgerOnTwoHosts(t)
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	read := func() (map[string]any, string) {
		t.Helper()
		response := mac.request(t, http.MethodGet, "/api/v1/ledger?projectKey="+widgetKey+"&view=work&all=true", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("read on the replica: %d %s", response.Code, response.Body)
		}
		var body map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		return body, response.Header().Get("ETag")
	}
	board := func() map[string]any {
		t.Helper()
		response := mac.request(t, http.MethodGet, "/api/v1/ledger?projectKey="+widgetKey, nil)
		var body map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		sync, _ := body["sync"].(map[string]any)
		return sync
	}

	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkAdd, Text: "add the index"}, nil)
	if work, _ := read(); !strings.Contains(fmt.Sprint(work["work"]), "add the index") {
		t.Fatalf("the replica does not read back its proxied write: %v", work)
	}
	if sync := board(); sync["owner"] != record.ID || sync["error"] != nil {
		t.Fatalf("replica sync after a good pull %v", sync)
	}

	owner.gate.refusePullsAfterWrite.Store(true)
	_, before := read()
	var added ledger.WorkResult
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkAdd, Text: "keep it fast"}, &added)
	if added.Item.ID == "" {
		t.Fatalf("the owner's answer was lost although it recorded the write: %+v", added)
	}
	sync := board()
	if errText, _ := sync["error"].(string); errText == "" {
		t.Fatalf("a failed pull after the write is not shown: %v", sync)
	}
	if _, after := read(); after != before {
		t.Fatal("the replica's work changed without a pull")
	}

	owner.gate.refusePullsAfterWrite.Store(false)
	owner.gate.pullsRefused.Store(false)
	host, _ := mac.daemon.pairedHost(record.ID)
	mac.daemon.syncLedgerHost(context.Background(), host)
	if work, after := read(); after == before || !strings.Contains(fmt.Sprint(work["work"]), "keep it fast") {
		t.Fatalf("after the next sync the replica reads %v", work)
	}
	if sync := board(); sync["error"] != nil || sync["behind"] != 0.0 {
		t.Fatalf("replica sync after recovering %v", sync)
	}
}

func openLedgerStream(t *testing.T, handler http.Handler) func(within time.Duration) (ledgerChange, bool) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/ledger/events", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	changes := make(chan ledgerChange, 64)
	go func() {
		defer close(changes)
		reader := sse.NewReader(response.Body)
		for {
			event, err := reader.Next()
			if err != nil {
				return
			}
			var change ledgerChange
			if event.Type == "ledger.changed" && json.Unmarshal([]byte(event.Data), &change) == nil {
				changes <- change
			}
		}
	}()
	receive := func(within time.Duration) (ledgerChange, bool) {
		select {
		case change, open := <-changes:
			return change, open
		case <-time.After(within):
			return ledgerChange{}, false
		}
	}
	return receive
}

func TestTheLedgerStreamKeepsAReaderThatFallsBehindOnManyProjects(t *testing.T) {
	shortLedgerChangeWait(t)
	d, r, handler := ledgerDaemon(t)
	store := d.ledgerStore()
	const projects = 100
	var keys []string
	for i := range projects {
		key := fmt.Sprintf("many-%03d", i)
		addLedgerProject(t, r, "proj-"+key, key)
		keys = append(keys, key)
	}
	receive := openLedgerStream(t, handler)
	for _, key := range keys {
		addWork(t, store, key, "build the index")
	}
	time.Sleep(2 * ledgerChangeWait)
	latest := map[string]string{}
	for len(latest) < projects {
		change, ok := receive(5 * time.Second)
		if !ok {
			t.Fatalf("the stream ended or went quiet with %d of %d projects seen", len(latest), projects)
		}
		latest[change.ProjectKey] = change.Version
	}
	for _, key := range keys {
		if _, etag := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+key); latest[key] != etag {
			t.Fatalf("%s: the stream's version %s is not the board's ETag %s", key, latest[key], etag)
		}
	}
	addWork(t, store, keys[0], "keep it fast")
	if change, ok := receive(5 * time.Second); !ok || change.ProjectKey != keys[0] {
		t.Fatalf("the stream stopped after falling behind: %+v", change)
	}
}
