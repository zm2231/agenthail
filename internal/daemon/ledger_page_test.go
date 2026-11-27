package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/nodehost"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sse"
	"github.com/zm2231/agenthail/internal/surface"
)

func ledgerPageDaemon(t *testing.T) (*Daemon, ledgerProject, http.Handler, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, r, handler := ledgerDaemon(t)
	home := extensionTestHome(t)
	d.SetExtensionHost(home, func() (nodehost.Launch, error) {
		return nodehost.Launch{}, errors.New("no extension host in this test")
	})
	t.Cleanup(d.extensions.mounts.close)
	if err := d.syncExtensionMounts(); err != nil {
		t.Fatal(err)
	}
	page, ok := d.extensionsView().BuiltIn[extension.Ledger]
	if !ok || page.Port == 0 || page.Entry != "/" {
		t.Fatalf("the ledger page has no port: %+v", d.extensionsView())
	}
	return d, addLedgerProject(t, r, "proj-widget", "repo:github.com/acme/widget"), handler, fmt.Sprintf("http://127.0.0.1:%d", page.Port)
}

func ledgerPageRead(t *testing.T, base, project, token, etag string) (*http.Response, ledgerPageView) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, base+"/ledger?project="+project, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var view ledgerPageView
	if response.StatusCode == http.StatusOK {
		if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
			t.Fatal(err)
		}
	}
	return response, view
}

func work(t *testing.T, store *ledger.Store, req ledger.WorkRequest) ledger.WorkItem {
	t.Helper()
	result, err := store.Work(req, time.Now())
	if err != nil {
		t.Fatalf("%s %s: %v", req.Action, req.ItemID, err)
	}
	return result.Item
}

func TestTheWorkPageShowsAProjectsWorkByStateWithWhoHoldsItAndWhatItIsFor(t *testing.T) {
	d, p, handler, base := ledgerPageDaemon(t)
	r := d.Registry
	p.session(t, r, "s1")
	p.session(t, r, "s2")
	if err := r.SetAlias("builder", "s1"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("checker", "s2"); err != nil {
		t.Fatal(err)
	}
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	ref := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "s1", PromptID: "p-1", Prompt: "find old orders fast"}).Ref
	ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: p.key, SessionID: "s1", Name: "Search"}, nil)
	ledgerWrite(t, handler, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: p.key, SessionID: "s1", UtteranceID: ref, As: ledger.AsNewAsk, To: "widget-G1", Clauses: []string{"find old orders fast"}}, nil)

	store := d.ledgerStore()
	building := work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "Build the order index", Fields: ledger.WorkFields{Type: ledger.WorkFeature, Priority: "P1", Description: "Index orders by customer and date\nso search reads one table."}})
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkClaim, ItemID: building.ID})
	reviewed := work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkAdd, Text: "Drop the old search"})
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkClaim, ItemID: reviewed.ID})
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkSubmit, ItemID: reviewed.ID})
	blocked := work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s2", Action: ledger.WorkAdd, Text: "Move search to the new host"})
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s2", Action: ledger.WorkBlock, ItemID: blocked.ID, Reason: "the host has no disk yet"})
	later := work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s2", Action: ledger.WorkAdd, Text: "Search by phone number"})
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s2", Action: ledger.WorkPark, ItemID: later.ID, Park: &ledger.Park{Kind: ledger.ParkLater, Why: "after the index ships"}})
	asked := work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s2", Action: ledger.WorkAdd, Text: "Keep deleted orders searchable"})
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s2", Action: ledger.WorkClaim, ItemID: asked.ID})
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s2", Action: ledger.WorkPark, ItemID: asked.ID, Park: &ledger.Park{Kind: ledger.ParkPerson, Person: &ledger.ParkQuestion{Question: "Should deleted orders stay searchable for 30 days?"}}})
	next := work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s2", Action: ledger.WorkAdd, Text: "Tidy the search page", Fields: ledger.WorkFields{Priority: "P3"}})
	urgent := work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s2", Action: ledger.WorkAdd, Text: "Fix the search crash", Fields: ledger.WorkFields{Priority: "P0"}})
	shipped := work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkAdd, Text: "Add the search box"})
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkClaim, ItemID: shipped.ID})
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkSubmit, ItemID: shipped.ID})
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s2", Action: ledger.WorkApprove, ItemID: shipped.ID})
	gone := work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkAdd, Text: "A mistake"})
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkDelete, ItemID: gone.ID, Reason: "added by mistake"})

	if response, _ := ledgerPageRead(t, base, "proj-widget", "", ""); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the work page answered %d without credentials", response.StatusCode)
	}
	response, view := ledgerPageRead(t, base, "proj-widget", "test-token", "")
	if response.StatusCode != http.StatusOK || view.Key != p.key {
		t.Fatalf("the work page answered %d for %s", response.StatusCode, view.Key)
	}
	ids := func(rows []ledgerPageRow) string {
		var out []string
		for _, row := range rows {
			out = append(out, row.ID)
		}
		return strings.Join(out, ",")
	}
	for state, want := range map[ledger.WorkState]string{
		ledger.WorkOpen:   urgent.ID + "," + next.ID,
		ledger.WorkParked: later.ID,
	} {
		if got := ids(view.Work[string(state)]); got != want {
			t.Errorf("%s shows %q, want %q", state, got, want)
		}
	}
	for lane, want := range map[string]string{laneIdle: building.ID, laneReview: reviewed.ID, laneBlocked: blocked.ID} {
		if got := ids(view.Board[lane]); got != want {
			t.Errorf("the %s lane shows %q, want %q", lane, got, want)
		}
	}
	row := view.Board[laneIdle][0]
	if row.Title != "Build the order index" || row.What != "Index orders by customer and date" || row.For != "find old orders fast" || row.Holder != "@builder" || row.Priority != "P1" || row.Type != "feature" || row.Since.IsZero() || row.Flight.HolderState != holderIdle {
		t.Errorf("an item in progress reads %+v %+v", row, row.Flight)
	}
	if row := view.Board[laneBlocked][0]; row.Note != "the host has no disk yet" || row.Holder != "" {
		t.Errorf("a blocked item reads %+v", row)
	}
	if row := view.Work[string(ledger.WorkParked)][0]; row.Parked == nil || row.Parked.Kind != "later" || row.Parked.Why != "after the index ships" {
		t.Errorf("a parked item reads %+v", row)
	}
	if len(view.Waiting) != 1 || view.Waiting[0].Question != "Should deleted orders stay searchable for 30 days?" || view.Waiting[0].From != "@checker" || view.Waiting[0].Work == nil || view.Waiting[0].Work.ID != asked.ID {
		t.Errorf("waiting on the person: %+v", view.Waiting)
	}
	if len(view.Done) != 1 || view.Done[0].ID != shipped.ID || view.Done[0].Reviewer != "@checker" {
		t.Errorf("recently done: %+v", view.Done)
	}
	if phases := []ledger.Phase{view.Board[laneIdle][0].Phase, view.Board[laneReview][0].Phase, view.Work[string(ledger.WorkOpen)][0].Phase, view.Done[0].Phase}; !slices.Equal(phases, []ledger.Phase{ledger.PhaseBuild, ledger.PhaseCheck, ledger.PhaseScope, ledger.PhaseShip}) || view.Phases[ledger.PhaseCheck] != "Check" || len(view.Phases) != len(ledger.Phases) {
		t.Errorf("phases %v with labels %v, want build, check, scope and ship under the default names", phases, view.Phases)
	}

	etag := response.Header.Get("ETag")
	if again, _ := ledgerPageRead(t, base, "proj-widget", "test-token", etag); again.StatusCode != http.StatusNotModified {
		t.Fatalf("an unchanged ledger answered %d to its own ETag", again.StatusCode)
	}
	if byKey, keyed := ledgerPageRead(t, base, p.key, "test-token", ""); byKey.StatusCode != http.StatusOK || keyed.Key != p.key || len(keyed.Done) != 1 {
		t.Fatalf("the repository's project id reads %d %+v", byKey.StatusCode, keyed)
	}
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s2", Action: ledger.WorkClaim, ItemID: next.ID})
	if changed, view := ledgerPageRead(t, base, "proj-widget", "test-token", etag); changed.StatusCode != http.StatusOK || len(view.Board[laneIdle]) != 2 || ids(view.Work[string(ledger.WorkOpen)]) != urgent.ID {
		t.Fatalf("after a claim the page answered %d with %+v %+v", changed.StatusCode, view.Board, view.Work)
	}
}

func TestTheWorkPageListsSetAsideWorkThatIsReadyToPickUpFirst(t *testing.T) {
	d, p, _, base := ledgerPageDaemon(t)
	p.session(t, d.Registry, "s1")
	store := d.ledgerStore()
	due := work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkAdd, Text: "Retry the export"})
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkPark, ItemID: due.ID, Park: &ledger.Park{Kind: ledger.ParkLater, Why: "after the freeze", Revisit: time.Now().Add(-time.Hour)}})
	waiting := work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkAdd, Text: "Rename the tables"})
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s1", Action: ledger.WorkPark, ItemID: waiting.ID, Park: &ledger.Park{Kind: ledger.ParkLater, Why: "next quarter", Revisit: time.Now().Add(72 * time.Hour)}})

	_, view := ledgerPageRead(t, base, "proj-widget", "test-token", "")
	if len(view.Ready) != 1 || view.Ready[0].ID != due.ID || view.Ready[0].Parked == nil || view.Ready[0].Parked.Ready != "its revisit date came" || view.Ready[0].Parked.Why != "after the freeze" {
		t.Fatalf("ready to pick up: %+v", view.Ready)
	}
	if parked := view.Work[string(ledger.WorkParked)]; len(parked) != 1 || parked[0].ID != waiting.ID || parked[0].Parked.Ready != "" {
		t.Fatalf("set aside: %+v", parked)
	}
}

func TestTheWorkPageShowsOnlyItsOwnProjectAndAnEmptyListForOneWithNoWork(t *testing.T) {
	d, p, _, base := ledgerPageDaemon(t)
	other := addLedgerProject(t, d.Registry, "proj-other", "repo:github.com/acme/other")
	addWork(t, d.ledgerStore(), other.key, "other work")
	addWork(t, d.ledgerStore(), p.key, "widget work")
	_, view := ledgerPageRead(t, base, "proj-widget", "test-token", "")
	if open := view.Work[string(ledger.WorkOpen)]; len(open) != 1 || open[0].Title != "widget work" {
		t.Fatalf("the widget page lists %+v", open)
	}
	for _, project := range []string{"proj-none", "repo:github.com/acme/none"} {
		response, empty := ledgerPageRead(t, base, project, "test-token", "")
		if response.StatusCode != http.StatusOK || empty.Key != "" || len(empty.Work[string(ledger.WorkOpen)]) != 0 || empty.Waiting == nil || empty.Done == nil {
			t.Fatalf("%s answered %d %+v", project, response.StatusCode, empty)
		}
	}
	if response, _ := ledgerPageRead(t, base, "", "test-token", ""); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("no project answered %d", response.StatusCode)
	}
}

func openWorkPageEvents(t *testing.T, base, project string) <-chan ledgerChange {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events?project="+project, nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != http.StatusOK {
		t.Fatalf("events for %s answered %d", project, response.StatusCode)
	}
	changes := make(chan ledgerChange, 8)
	go func() {
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
	return changes
}

func TestTheWorkPageHearsItsOwnProjectsChangesOnly(t *testing.T) {
	shortLedgerChangeWait(t)
	d, p, _, base := ledgerPageDaemon(t)
	other := addLedgerProject(t, d.Registry, "proj-other", "repo:github.com/acme/other")
	fresh := addLedgerProject(t, d.Registry, "proj-fresh", "repo:github.com/acme/fresh")
	store := d.ledgerStore()
	addWork(t, store, p.key, "widget work")
	widget := openWorkPageEvents(t, base, "proj-widget")
	unrecorded := openWorkPageEvents(t, base, "proj-fresh")
	addWork(t, store, other.key, "other work")
	addWork(t, store, p.key, "more widget work")
	addWork(t, store, fresh.key, "the first fresh work")
	for name, heard := range map[string]struct {
		changes <-chan ledgerChange
		key     string
	}{"widget": {widget, p.key}, "fresh": {unrecorded, fresh.key}} {
		select {
		case change := <-heard.changes:
			if change.ProjectKey != heard.key {
				t.Fatalf("the %s page heard %+v", name, change)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the %s page heard nothing after its project changed", name)
		}
	}
	select {
	case change := <-widget:
		t.Fatalf("the widget page heard a second change %+v", change)
	case <-time.After(3 * ledgerChangeWait):
	}
}

func TestAProjectsSubscriberIsNotWokenForOtherProjects(t *testing.T) {
	hub := newLedgerChangeHub(time.Millisecond, func(key string) (string, error) { return "v-" + key, nil }, t.Logf)
	id, wake, cancel := hub.subscribe("mine")
	defer cancel()
	for i := range 50 {
		hub.publish(fmt.Sprintf("other-%d", i))
	}
	select {
	case <-wake:
		t.Fatalf("woken for other projects, holding %v", hub.take(id))
	default:
	}
	hub.publish("mine")
	<-wake
	if changes := hub.take(id); len(changes) != 1 || changes[0].ProjectKey != "mine" {
		t.Fatalf("took %+v", changes)
	}
}

func TestASlowEarlierPublishDoesNotReplaceANewerVersion(t *testing.T) {
	reading, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	hub := newLedgerChangeHub(time.Hour, func(string) (string, error) {
		if reads.Add(1) == 1 {
			close(reading)
			<-release
			return "old", nil
		}
		return "new", nil
	}, t.Logf)
	id, wake, cancel := hub.subscribe("")
	defer cancel()
	slow := make(chan struct{})
	go func() {
		defer close(slow)
		hub.publish("proj")
	}()
	<-reading
	hub.publish("proj")
	<-wake
	if changes := hub.take(id); len(changes) != 1 || changes[0].Version != "new" {
		t.Fatalf("took %+v", changes)
	}
	close(release)
	<-slow
	if changes := hub.take(id); len(changes) != 0 {
		t.Fatalf("the slow earlier publish delivered %+v after the newer version", changes)
	}
}

func TestTheWorkPageIsServedOnItsOwnPortAndNotListedAsAnApp(t *testing.T) {
	d, _, handler, base := ledgerPageDaemon(t)
	dir := filepath.Join(extension.Workspace(d.extensions.home), extension.Ledger)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"extension.json": `{"label": "Mine"}`, "server.ts": `export default () => {}`, "ui.html": `<p>mine</p>`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.syncExtensionMounts(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/extensions", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	handler.ServeHTTP(w, request)
	var listed extensionsView
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || w.Code != http.StatusOK {
		t.Fatalf("extensions answered %d %s", w.Code, w.Body)
	}
	for _, ext := range listed.Extensions {
		if ext.ID == extension.Ledger {
			t.Fatalf("a folder named %s became an extension: %+v", extension.Ledger, ext)
		}
	}
	if page := listed.BuiltIn[extension.Ledger]; fmt.Sprintf("http://127.0.0.1:%d", page.Port) != base {
		t.Fatalf("the built-in page moved to %+v from %s", page, base)
	}
	response, body := requestTo(t, base, http.MethodGet, "/", "test-token", nil)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `src="ledger.js"`) || !strings.Contains(response.Header.Get("Content-Security-Policy"), "frame-ancestors") {
		t.Fatalf("the page answered %d %v %.200s", response.StatusCode, response.Header, body)
	}
	for _, path := range []string{"/ledger.js", "/ledger-text.js", "/ledger.css"} {
		response, _ := requestTo(t, base, http.MethodGet, path, "test-token", nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s answered %d", path, response.StatusCode)
		}
		if strings.HasSuffix(path, ".js") && !strings.Contains(response.Header.Get("Content-Type"), "javascript") {
			t.Fatalf("%s is served as %q, which a module script will not run", path, response.Header.Get("Content-Type"))
		}
	}
	if response, _ := requestTo(t, base, http.MethodGet, "/", "", nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the page answered %d without credentials", response.StatusCode)
	}
}

func TestTheWorkPageOfARepositoryOpensFromAnyCheckoutTheAppsListItUnder(t *testing.T) {
	d, _, _, base := ledgerPageDaemon(t)
	const key = "repo:github.com/acme/gadget"
	record := func(id, hostID, localID string) {
		t.Helper()
		project := fmt.Sprintf(`{"id":%q,"localId":%q,"displayName":"gadget","rootPath":"/work/gadget","kind":"git","key":%q}`, key, localID, key)
		session := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: "/work/gadget", Status: surface.StatusIdle, LastActive: time.Now()}
		if _, _, err := d.Registry.RecordCatalogSession(registry.CatalogSessionState{Session: session, HostID: hostID, Project: []byte(project), Checkout: []byte(`{}`), ObservedAt: time.Now().UTC(), ProjectionFingerprint: `{}`}, registry.CatalogEvent{DedupeKey: "session.upserted:" + id, Type: "session.upserted", EntityID: id, Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	record("here", "", "implicit:/work/gadget")
	record("there", "peer-host", "peer-host::implicit:/srv/gadget")
	item := work(t, d.ledgerStore(), ledger.WorkRequest{ProjectKey: key, SessionID: "here", Action: ledger.WorkAdd, Text: "Ship the gadget"})

	for _, project := range []string{key, "implicit:/work/gadget", d.hostIdentity().ID + "::implicit:/work/gadget", "peer-host::implicit:/srv/gadget"} {
		response, view := ledgerPageRead(t, base, url.QueryEscape(project), "test-token", "")
		if response.StatusCode != http.StatusOK || view.Key != key || len(view.Work[string(ledger.WorkOpen)]) != 1 || view.Work[string(ledger.WorkOpen)][0].ID != item.ID {
			t.Fatalf("the work page for %s answered %d with %s: %+v", project, response.StatusCode, view.Key, view.Work)
		}
	}
	if response, view := ledgerPageRead(t, base, url.QueryEscape("implicit:/work/elsewhere"), "test-token", ""); response.StatusCode != http.StatusOK || view.Key != "" {
		t.Fatalf("a checkout no session was seen in read %q", view.Key)
	}
}
