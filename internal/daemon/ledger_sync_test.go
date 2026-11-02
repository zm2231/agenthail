package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	if err := ledger.NewStore(mac.reg.WriteDB(), mac.reg.ReadDB()).Enable(widgetKey); err != nil {
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

func eventLog(t *testing.T, reg *registry.Registry, order string) (string, int) {
	t.Helper()
	rows, err := reg.ReadDB().Query(`SELECT `+order+`, actor_host_id, origin_seq, type, payload, actor_session_id, ref, created_at FROM ledger_events WHERE project_key=? AND `+order+`>0 ORDER BY `+order, widgetKey)
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
	if err := reg.ReadDB().QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=? AND `+order+`=0`, widgetKey).Scan(&unsequenced); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n"), unsequenced
}

func boardView(t *testing.T, reg *registry.Registry) string {
	t.Helper()
	board, err := ledger.NewStore(reg.WriteDB(), reg.ReadDB()).Board(widgetKey)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"goals": board.Goals(), "asks": board.AllAsks(), "work": board.Ready(), "questions": board.Questions(), "utterances": board.Utterances()})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func sameLedgerOnBothHosts(t *testing.T, mac, owner *federationNode) {
	t.Helper()
	var replica, authoritative string
	var waiting int
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("last compared logs, %d mac events without an owner seq\nmac:\n%s\nowner:\n%s", waiting, replica, authoritative)
		}
	})
	testkit.Eventually(t, "the mac's log matches the owner's", 20*time.Second, func() bool {
		replica, waiting = eventLog(t, mac.reg, "owner_seq")
		authoritative, _ = eventLog(t, owner.reg, "seq")
		return waiting == 0 && replica == authoritative
	})
	if got, want := boardView(t, mac.reg), boardView(t, owner.reg); got != want {
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
	sameLedgerOnBothHosts(t, mac, owner)

	var added ledger.WorkResult
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "add the index"}, &added)
	if added.Item.ID != "widget-W1" {
		t.Fatalf("proxied add = %+v", added)
	}
	if !strings.Contains(boardView(t, mac.reg), "add the index") {
		t.Fatal("the mac does not read its own proxied write back")
	}
	mustLedger(t, owner, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "remote-a", Action: ledger.WorkComment, ItemID: "widget-W1", Text: "written on the owner"}, nil)
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkClaim, ItemID: "widget-W1"}, nil)
	capturePrompt(t, mac, "local-a", "p-2", "and keep it fast")
	sameLedgerOnBothHosts(t, mac, owner)

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
	sameLedgerOnBothHosts(t, mac, owner)
}

func TestTheLargestStorableWordsReplayAndLargerOnesBlockNothing(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	capturePrompt(t, mac, "local-a", "p-1", "build search")
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	sameLedgerOnBothHosts(t, mac, owner)

	huge := capturePrompt(t, mac, "local-a", "p-huge", strings.Repeat("a", 2*ledger.MaxEventBytes))
	big := capturePrompt(t, mac, "local-a", "p-big", strings.Repeat("b", ledger.MaxEventBytes-2048))
	after := capturePrompt(t, mac, "local-a", "p-after", "and after it")
	sameLedgerOnBothHosts(t, mac, owner)
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
	sameLedgerOnBothHosts(t, mac, owner)

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
	sameLedgerOnBothHosts(t, mac, owner)
	if n := countEvents(t, owner.reg, ledger.EventUtterance, offline); n != 1 {
		t.Fatalf("the offline words landed %d times on the owner", n)
	}
	if n := countEvents(t, owner.reg, ledger.EventClause, "export to csv"); n != 1 {
		log, _ := eventLog(t, owner.reg, "seq")
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
