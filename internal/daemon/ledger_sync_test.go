package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/testkit"
)

const widgetKey = "repo:github.com/example/widget"

func ledgerOnTwoHosts(t *testing.T) (mac, owner *federationNode, record registry.PairedHost) {
	t.Helper()
	mac, owner, record = widgetOnTwoHosts(t)
	if err := ledger.NewStore(mac.reg.WriteDB(), mac.reg.ReadDB()).Enable(widgetKey, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	return mac, owner, record
}

func ledgerRequest(t *testing.T, node *federationNode, path string, body any) (int, string) {
	t.Helper()
	response := node.request(t, http.MethodPost, path, body)
	return response.Code, response.Body.String()
}

func mustLedger(t *testing.T, node *federationNode, path string, body, into any) {
	t.Helper()
	code, raw := ledgerRequest(t, node, path, body)
	if code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", path, code, raw)
	}
	if into != nil {
		if err := json.Unmarshal([]byte(raw), into); err != nil {
			t.Fatal(err)
		}
	}
}

func capturePrompt(t *testing.T, node *federationNode, session, prompt, text string) string {
	t.Helper()
	var captured ledgerCaptureResponse
	mustLedger(t, node, "/api/v1/ledger/capture", ledgerCaptureRequest{SessionID: session, PromptID: prompt, Prompt: text}, &captured)
	if !captured.Captured || captured.Ref == "" {
		t.Fatalf("capture %s: %+v", prompt, captured)
	}
	return captured.Ref
}

func eventLog(t *testing.T, key string, reg *registry.Registry, order string) (string, int) {
	t.Helper()
	rows, err := reg.ReadDB().Query(`SELECT `+order+`, actor_host_id, origin_seq, type, payload, actor_session_id, ref, created_at FROM ledger_events WHERE project_key=? AND `+order+`>0 ORDER BY `+order, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var seq, origin int64
		var host, typ, actor, ref, at string
		var payload []byte
		if err := rows.Scan(&seq, &host, &origin, &typ, &payload, &actor, &ref, &at); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, fmt.Sprintf("%d %s/%d %s %s %s %s %s", seq, host, origin, typ, payload, actor, ref, at))
	}
	var unsequenced int
	if err := reg.ReadDB().QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=? AND `+order+`=0`, key).Scan(&unsequenced); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n"), unsequenced
}

func boardView(t *testing.T, key string, reg *registry.Registry) string {
	t.Helper()
	board, err := ledger.NewStore(reg.WriteDB(), reg.ReadDB()).Board(key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"goals": board.Goals(), "asks": board.AllAsks(), "work": board.Ready(), "questions": board.Questions(), "utterances": board.Utterances()})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func sameLedgerOnBothHosts(t *testing.T, key string, mac, owner *federationNode) {
	t.Helper()
	var replica, authoritative string
	var waiting int
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("last compared logs, %d mac events without an owner seq\nmac:\n%s\nowner:\n%s", waiting, replica, authoritative)
		}
	})
	testkit.Eventually(t, "the mac's log matches the owner's", 20*time.Second, func() bool {
		replica, waiting = eventLog(t, key, mac.reg, "owner_seq")
		authoritative, _ = eventLog(t, key, owner.reg, "seq")
		return waiting == 0 && replica == authoritative
	})
	if got, want := boardView(t, key, mac.reg), boardView(t, key, owner.reg); got != want {
		t.Fatalf("boards differ\nmac   %s\nowner %s", got, want)
	}
}

func countEvents(t *testing.T, reg *registry.Registry, typ ledger.EventType, ref string) int {
	t.Helper()
	var n int
	if err := reg.ReadDB().QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=? AND type=? AND (?='' OR ref=? OR CAST(payload AS TEXT) LIKE '%'||?||'%')`, widgetKey, string(typ), ref, ref, ref).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestALedgerKeptOnAnotherHostReplicatesBothWays(t *testing.T) {
	mac, owner, record := ledgerOnTwoHosts(t)
	first := capturePrompt(t, mac, "local-a", "p-1", "build search")
	mustLedger(t, mac, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: widgetKey, SessionID: "local-a", Name: "Search"}, nil)
	mustLedger(t, mac, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: widgetKey, SessionID: "local-a", UtteranceID: first, As: ledger.AsNewAsk, To: "widget-G1", Clauses: []string{"build search"}}, nil)

	var assigned ledgerOwnerResponse
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, &assigned)
	if assigned.Owner != record.ID || !assigned.Adopted {
		t.Fatalf("owner = %+v", assigned)
	}
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)

	var added ledger.WorkResult
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "add the index"}, &added)
	if added.Item.ID != "widget-W1" {
		t.Fatalf("proxied add = %+v", added)
	}
	if !strings.Contains(boardView(t, widgetKey, mac.reg), "add the index") {
		t.Fatal("the mac does not read its own proxied write back")
	}
	mustLedger(t, owner, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "remote-a", Action: ledger.WorkComment, ItemID: "widget-W1", Text: "written on the owner"}, nil)
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkClaim, ItemID: "widget-W1"}, nil)
	capturePrompt(t, mac, "local-a", "p-2", "and keep it fast")
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)

	var item ledger.WorkItem
	board, _ := ledger.NewStore(mac.reg.WriteDB(), mac.reg.ReadDB()).Board(widgetKey)
	if item, _ = board.WorkItem("widget-W1"); item.Assignee != "local-a" || len(item.Timeline) != 3 {
		t.Fatalf("mac sees widget-W1 as %+v", item)
	}
}

func TestAProxiedWriteWhoseAnswerIsLostLandsOnce(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	first := capturePrompt(t, mac, "local-a", "p-1", "build search")
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	mustLedger(t, mac, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: widgetKey, SessionID: "local-a", Name: "Search"}, nil)
	mustLedger(t, mac, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: widgetKey, SessionID: "local-a", UtteranceID: first, As: ledger.AsNewAsk, To: "widget-G1", Clauses: []string{"build search"}}, nil)

	owner.gate.ledgerDrops.Store(true)
	owner.gate.drops.Store(2)
	var added ledger.WorkResult
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "add the index"}, &added)
	if owner.gate.drops.Load() != 0 {
		t.Fatalf("the owner's answers were not dropped: %d left", owner.gate.drops.Load())
	}
	if n := countEvents(t, owner.reg, ledger.EventWorkItem, ""); n != 1 || added.Item.ID != "widget-W1" {
		t.Fatalf("owner holds %d work items after a lost answer; result %+v", n, added)
	}
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)
}

func TestTheLargestStorableWordsReplayAndLargerOnesBlockNothing(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	capturePrompt(t, mac, "local-a", "p-1", "build search")
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)

	huge := capturePrompt(t, mac, "local-a", "p-huge", strings.Repeat("a", 2*ledger.MaxEventBytes))
	big := capturePrompt(t, mac, "local-a", "p-big", strings.Repeat("b", ledger.MaxEventBytes-2048))
	after := capturePrompt(t, mac, "local-a", "p-after", "and after it")
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)
	for _, ref := range []string{huge, big, after} {
		if n := countEvents(t, owner.reg, ledger.EventUtterance, ref); n != 1 {
			t.Fatalf("%s landed %d times on the owner", ref, n)
		}
	}
	board, err := ledger.NewStore(owner.reg.WriteDB(), owner.reg.ReadDB()).Board(widgetKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range board.Utterances() {
		if u.ID == huge && (u.Truncated == nil || u.Truncated.OriginalBytes != 2*ledger.MaxEventBytes) {
			t.Fatalf("the oversized prompt reached the owner without its truncation mark: %+v", u.Truncated)
		}
	}
}

func TestAMacOfflineCapturesThenAttachesOnceTheOwnerIsBack(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	first := capturePrompt(t, mac, "local-a", "p-1", "build search")
	mustLedger(t, mac, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: widgetKey, SessionID: "local-a", Name: "Search"}, nil)
	mustLedger(t, mac, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: widgetKey, SessionID: "local-a", UtteranceID: first, As: ledger.AsNewAsk, To: "widget-G1", Clauses: []string{"build search"}}, nil)
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)

	owner.gate.proxyDown.Store(true)
	offline := capturePrompt(t, mac, "local-a", "p-2", "and export to csv")
	attach := ledger.AttachRequest{ProjectKey: widgetKey, SessionID: "local-a", UtteranceID: offline, As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"export to csv"}}
	code, body := ledgerRequest(t, mac, "/api/v1/ledger/attach", attach)
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "vps") || !strings.Contains(body, "nothing was recorded") {
		t.Fatalf("attach with the owner down: status=%d body=%s", code, body)
	}
	if n := countEvents(t, owner.reg, ledger.EventClause, "export to csv"); n != 0 {
		t.Fatalf("the owner recorded %d clauses while down", n)
	}
	var views []ledgerSyncView
	testkit.Eventually(t, "doctor sees the backlog and the failing owner", 20*time.Second, func() bool {
		response := mac.request(t, http.MethodGet, "/api/v1/ledger/sync/status", nil)
		views = nil
		_ = json.Unmarshal(response.Body.Bytes(), &views)
		return len(views) == 1 && views[0].Unreplayed >= 1 && views[0].LastError != ""
	})

	owner.gate.proxyDown.Store(false)
	testkit.Eventually(t, "the attach goes through once the owner is back", 30*time.Second, func() bool {
		code, _ := ledgerRequest(t, mac, "/api/v1/ledger/attach", attach)
		return code == http.StatusOK
	})
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)
	if n := countEvents(t, owner.reg, ledger.EventUtterance, offline); n != 1 {
		t.Fatalf("the offline words landed %d times on the owner", n)
	}
	if n := countEvents(t, owner.reg, ledger.EventClause, "export to csv"); n != 1 {
		log, _ := eventLog(t, widgetKey, owner.reg, "seq")
		t.Fatalf("the attach landed %d times on the owner:\n%s", n, log)
	}
	testkit.Eventually(t, "doctor sees the sync healthy again", 20*time.Second, func() bool {
		response := mac.request(t, http.MethodGet, "/api/v1/ledger/sync/status", nil)
		views = nil
		_ = json.Unmarshal(response.Body.Bytes(), &views)
		return len(views) == 1 && views[0].Unreplayed == 0 && views[0].LastError == "" && views[0].Lag == 0 && views[0].SyncedAt != nil
	})
}

func TestARefusedOwnerAssignmentLeavesNeitherHostOwning(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	words, err := json.Marshal(ledger.UtterancePayload{Text: strings.Repeat("x", ledger.MaxEventBytes), UtteranceID: "claude-prompt:old", SessionID: "local-a", CapturedBy: ledger.CapturedByHook})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mac.reg.WriteDB().Exec(`INSERT INTO ledger_events(project_key,type,payload,actor_session_id,ref,created_at) VALUES(?,?,?,?,?,?)`, widgetKey, string(ledger.EventUtterance), words, "local-a", "claude-prompt:old", time.Now().UTC().Format("2006-01-02T15:04:05Z")); err != nil {
		t.Fatal(err)
	}
	if code, body := ledgerRequest(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}); code == http.StatusOK || !strings.Contains(body, "cannot sync") {
		t.Fatalf("owner assignment over an unreplayable event: status=%d body=%s", code, body)
	}
	for name, node := range map[string]*federationNode{"mac": mac, "owner": owner} {
		var n int
		if err := node.reg.ReadDB().QueryRow(`SELECT COUNT(*) FROM ledger_state WHERE project_key=? AND owner_host_id!=''`, widgetKey).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s records an owner after the refusal", name)
		}
	}
}

func TestAReplicaShowsTheWordsLeftToAttachAfterItsWritesGoToTheOwner(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	if err := ledger.NewStore(mac.reg.WriteDB(), mac.reg.ReadDB()).Enable(widgetKey, ledger.AttachAtCheckpoints); err != nil {
		t.Fatal(err)
	}
	words := capturePrompt(t, mac, "local-a", "p-1", "build search for the docs")
	checkpoint := func() string {
		t.Helper()
		var out ledgerCheckpointResponse
		mustLedger(t, mac, "/api/v1/ledger/checkpoint", ledgerCheckpointRequest{SessionID: "local-a"}, &out)
		return out.Instruction
	}
	var added ledger.WorkResult
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkAdd, Text: "index the docs"}, &added)
	if added.Item.ID != "widget-W1" || countEvents(t, owner.reg, ledger.EventWorkItem, "") != 1 {
		t.Fatalf("the add did not go through the owner: %+v", added)
	}
	if text := checkpoint(); !strings.Contains(text, words+": build search for the docs") {
		t.Fatalf("the replica's checkpoint after a proxied add = %q", text)
	}
	mustLedger(t, mac, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: widgetKey, SessionID: "local-a", Name: "Search"}, nil)
	var batch ledger.AttachEachResult
	mustLedger(t, mac, "/api/v1/ledger/attach", map[string]any{"projectKey": widgetKey, "sessionId": "local-a", "fragments": []ledger.AttachRequest{
		{UtteranceID: words, As: ledger.AsNewAsk, To: "widget-G1", Clauses: []string{"build search for the docs"}}}}, &batch)
	if len(batch.Fragments) != 1 || batch.Fragments[0].Refused != "" || countEvents(t, owner.reg, ledger.EventAttach, "") != 1 {
		t.Fatalf("the batched attach did not go through the owner: %+v", batch)
	}
	if text := checkpoint(); text != "" {
		t.Fatalf("the replica still shows words the owner has attached: %q", text)
	}
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)
	written := func(node *federationNode) (bySelf, byOthers int) {
		t.Helper()
		var self string
		if err := node.reg.ReadDB().QueryRow(`SELECT host_id FROM host_identity WHERE id=1`).Scan(&self); err != nil || self == "" {
			t.Fatalf("host identity %q: %v", self, err)
		}
		if err := node.reg.ReadDB().QueryRow(`SELECT COALESCE(SUM(actor_host_id=?),0), COALESCE(SUM(actor_host_id<>?),0) FROM ledger_events WHERE project_key=? AND type IN (?,?)`,
			self, self, widgetKey, string(ledger.EventWorkItem), string(ledger.EventAttach)).Scan(&bySelf, &byOthers); err != nil {
			t.Fatal(err)
		}
		return bySelf, byOthers
	}
	if bySelf, byOthers := written(owner); bySelf != 2 || byOthers != 0 {
		t.Fatalf("the owner holds %d work item and attach events it wrote and %d another host wrote; want 2 and 0", bySelf, byOthers)
	}
	if bySelf, byOthers := written(mac); bySelf != 0 || byOthers != 2 {
		t.Fatalf("the replica holds %d work item and attach events it wrote and %d from the owner; want 0 and 2", bySelf, byOthers)
	}
}

func TestAKeylessProjectsLedgerReplicatesToItsOwner(t *testing.T) {
	mac, owner, record := pairedFederation(t)
	path := plainFolder(t)
	id := addFolderProject(t, mac.daemon, path)
	moveSession(t, mac, "local-a", path, time.Now())
	key := ledgerKeyAt(t, mac.reg, path)
	if key != registry.LedgerProjectKey(id) {
		t.Fatalf("ledger key = %q", key)
	}
	if err := ledger.NewStore(mac.reg.WriteDB(), mac.reg.ReadDB()).Enable(key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	first := capturePrompt(t, mac, "local-a", "p-1", "keep notes")
	var assigned ledgerOwnerResponse
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: key, Host: "vps"}, &assigned)
	if assigned.Owner != record.ID || !assigned.Adopted {
		t.Fatalf("owner = %+v", assigned)
	}
	mustLedger(t, mac, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: key, SessionID: "local-a", Name: "Notes"}, nil)
	capturePrompt(t, mac, "local-a", "p-2", "and tidy them")
	sameLedgerOnBothHosts(t, key, mac, owner)
	if !strings.Contains(boardView(t, key, owner.reg), first) {
		t.Fatal("the owner's board lacks the words captured before it owned the ledger")
	}
}

func TestAKeylessLedgerWithOnlyAnOwnerIsNotLinkedAway(t *testing.T) {
	mac, _, _ := pairedFederation(t)
	path := plainFolder(t)
	id := addFolderProject(t, mac.daemon, path)
	other := addFolderProject(t, mac.daemon, plainFolder(t))
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: ledgerKeyAt(t, mac.reg, path), Host: "vps"}, nil)
	refused := runDashboardAction(t, mac.daemon, `{"action":"project-link","projectId":"`+id+`","survivor":"`+other+`"}`)
	if refused.Code != http.StatusConflict || apiErrorCode(t, refused) != "project_history" {
		t.Fatalf("linking away a project whose ledger has an owner: %d %s", refused.Code, refused.Body)
	}
}

func TestABurstOfCapturesReachesTheOwnerWithOneMoreSyncAtMost(t *testing.T) {
	mac, owner, record := widgetOnTwoHostsWith(t, func(f *federation) { f.ledgerSyncEvery = time.Hour })
	var assigned ledgerOwnerResponse
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, &assigned)
	if assigned.Owner != record.ID {
		t.Fatalf("owner = %+v", assigned)
	}
	host, found := mac.daemon.pairedHost(record.ID)
	if !found {
		t.Fatal("the mac lost its paired host")
	}
	replays := &snapshotHold{release: make(chan struct{})}
	owner.gate.replayHold.Store(replays)
	var once sync.Once
	release := func() {
		once.Do(func() {
			owner.gate.replayHold.Store(nil)
			close(replays.release)
		})
	}
	t.Cleanup(release)
	captureOf(t, mac, "lead", "p-0", "the first words")
	testkit.Eventually(t, "the first capture's sync to reach the owner", 10*time.Second, func() bool { return replays.waiting.Load() == 1 })
	before := owner.gate.ledgerPulls.Load()
	for i := 1; i <= 20; i++ {
		captureOf(t, mac, "lead", fmt.Sprintf("p-%d", i), fmt.Sprintf("words %d", i))
	}
	if pending := len(host.ledgerWake); pending != 1 {
		t.Fatalf("20 captures during one sync round left %d wakes pending; they should leave one", pending)
	}
	release()
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)
	// A round started by a wake queued behind every earlier one finds them all
	// done when it blocks on its pull.
	pulls := &snapshotHold{release: make(chan struct{})}
	owner.gate.ledgerPullHold.Store(pulls)
	t.Cleanup(func() {
		owner.gate.ledgerPullHold.Store(nil)
		close(pulls.release)
	})
	mac.daemon.wakeLedgerSync(host)
	testkit.Eventually(t, "a last sync round to reach the owner", 10*time.Second, func() bool { return pulls.waiting.Load() == 1 })
	if rounds := owner.gate.ledgerPulls.Load() - before - 1; rounds > 2 {
		t.Fatalf("21 captures made %d sync rounds after the first; a burst should add one at most besides the round it held", rounds)
	}
}

func TestWordsCapturedWhileTheOwnerIsDownReachItWhenItIsBack(t *testing.T) {
	mac, owner, record := widgetOnTwoHostsWith(t, func(f *federation) { f.ledgerSyncEvery = time.Hour })
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)
	owner.gate.proxyDown.Store(true)
	owner.server.CloseClientConnections()
	testkit.Eventually(t, "the mac to show the owner down", 20*time.Second, func() bool { return !mac.daemon.hostConnected(record.ID) })
	offline := capturePrompt(t, mac, "lead", "p-1", "while the owner is away")
	owner.gate.proxyDown.Store(false)
	testkit.Eventually(t, "the words to reach the owner once it is back", 20*time.Second, func() bool {
		return countEvents(t, owner.reg, ledger.EventUtterance, offline) == 1
	})
}
