package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func sessionIDs(t *testing.T, listing map[string]any) []string {
	t.Helper()
	rows, _ := listing["sessions"].([]any)
	ids := []string{}
	for _, row := range rows {
		ids = append(ids, row.(map[string]any)["id"].(string))
	}
	return ids
}

func TestMCPTokenSeesOnlyItsProjectAndItsChildren(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	alpha := addLedgerProject(t, r, "p-alpha", "alpha")
	beta := addLedgerProject(t, r, "p-beta", "beta")
	lead := alpha.session(t, r, "alpha-lead")
	alpha.session(t, r, "alpha-mate")
	stranger := beta.session(t, r, "beta-one")
	child := beta.session(t, r, "alpha-child")
	if err := r.RecordSessionOrigin(registry.SessionOrigin{SessionID: child.ID, Via: registry.OriginAgent, CreatorID: lead.ID}); err != nil {
		t.Fatal(err)
	}
	server := mcpServer(t, d)
	leadMCP := mcpSession(t, server, issueMCPToken(t, d, lead))
	cases := map[string][]string{
		"all-visible": {"alpha-child", "alpha-lead", "alpha-mate"},
		"project":     {"alpha-lead", "alpha-mate"},
		"mine":        {"alpha-child"},
	}
	for filter, want := range cases {
		listing, refused := callMCP(t, leadMCP, "agenthail_sessions", map[string]any{"filter": filter})
		got := sessionIDs(t, listing)
		if refused || strings.Join(sorted(got), ",") != strings.Join(want, ",") {
			t.Fatalf("%s: got %v refused=%v listing=%v", filter, got, refused, listing)
		}
	}
	if refusal, refused := callMCP(t, leadMCP, "agenthail_read", map[string]any{"sessionId": stranger.ID, "since": 0}); !refused || refusal["code"] != "out_of_scope" {
		t.Fatalf("read beta=%v refused=%v", refusal, refused)
	}
	for _, readable := range []string{"alpha-mate", "alpha-child", lead.ID} {
		if out, refused := callMCP(t, leadMCP, "agenthail_read", map[string]any{"sessionId": readable, "since": 0}); refused {
			t.Fatalf("read %s refused: %v", readable, out)
		}
	}
	strangerMCP := mcpSession(t, server, issueMCPToken(t, d, stranger))
	listing, _ := callMCP(t, strangerMCP, "agenthail_sessions", nil)
	if got := sessionIDs(t, listing); strings.Join(sorted(got), ",") != "alpha-child,beta-one" {
		t.Fatalf("beta sees %v", got)
	}
	for _, other := range []string{"alpha-lead", "alpha-mate"} {
		if refusal, refused := callMCP(t, strangerMCP, "agenthail_read", map[string]any{"sessionId": other, "since": 0}); !refused || refusal["code"] != "out_of_scope" {
			t.Fatalf("beta read %s=%v", other, refusal)
		}
	}
	if refusal, refused := callMCP(t, strangerMCP, "agenthail_read", map[string]any{"sessionId": "missing", "since": 0}); !refused || refusal["code"] != "not_found" {
		t.Fatalf("missing=%v", refusal)
	}
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestMCPReadPagesALargeSessionUnderTheCap(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	reply := strings.Repeat("r", 3900)
	for turn := range 25 {
		id := fmt.Sprintf("t%02d", turn)
		at := time.Date(2026, 11, 2, 10, turn, 0, 0, time.UTC).Format(time.RFC3339Nano)
		appendJournalPayloads(t, r, from.ID,
			sessionJournalPayload{ItemID: id + "-prompt", Kind: "message", Role: "user", Body: "question " + id, TurnID: id, TS: at},
			sessionJournalPayload{ItemID: id + "-reply", Kind: "message", Role: "assistant", Body: reply, TurnID: id, TS: at, Final: true})
	}
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, from))
	var seen []string
	cursor := ""
	for page := 0; ; page++ {
		args := map[string]any{"sessionId": from.ID, "limit": 50}
		if cursor != "" {
			args["before"] = cursor
		}
		result, err := session.CallTool(t.Context(), callParams("agenthail_read", args))
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(result.StructuredContent)
		if result.IsError || len(encoded) > mcpOutputLimit {
			t.Fatalf("page %d: %d bytes isError=%v %s", page, len(encoded), result.IsError, encoded[:min(len(encoded), 300)])
		}
		var out struct {
			Items      []mcpItemView `json:"items"`
			NextCursor string        `json:"nextCursor"`
		}
		if err := json.Unmarshal(encoded, &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Items) == 0 && out.NextCursor != "" {
			t.Fatalf("page %d is empty but continues", page)
		}
		ids := []string{}
		for _, item := range out.Items {
			ids = append(ids, item.ID)
		}
		seen = append(ids, seen...)
		if out.NextCursor == "" {
			break
		}
		if page > 20 {
			t.Fatal("paging did not end")
		}
		cursor = out.NextCursor
	}
	if len(seen) != 50 || seen[0] != "t00-prompt" || seen[49] != "t24-reply" {
		t.Fatalf("read %d items: first=%v last=%v", len(seen), seen[:min(2, len(seen))], seen[max(0, len(seen)-2):])
	}
	for index := 1; index < len(seen); index++ {
		if seen[index] == seen[index-1] {
			t.Fatalf("item %s read twice", seen[index])
		}
	}
	forward, refused := callMCP(t, session, "agenthail_read", map[string]any{"sessionId": from.ID, "since": 0})
	if refused || forward["truncated"] != true || forward["latestSeq"].(float64) == 0 {
		t.Fatalf("since=0: %v", forward["latestSeq"])
	}
}

func TestMCPReadClipsALongItemAndPagesItsBody(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	full := strings.Repeat("é", 15000)
	payload, _ := json.Marshal(sessionJournalPayload{ItemID: "long", Kind: "message", Role: "assistant", Body: full[:8000], Truncated: true, BodyRef: "body-long", Version: 1, Op: "upsert", TS: time.Now().Format(time.RFC3339Nano)})
	if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "message", ProviderKey: "long", Payload: payload, ObservedAt: time.Now(), BodyRef: "body-long", FullBody: []byte(full)}, registry.SessionJournalRetention{Count: 4096, Bytes: 64 << 20}); err != nil {
		t.Fatal(err)
	}
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, from))
	page, refused := callMCP(t, session, "agenthail_read", map[string]any{"sessionId": from.ID, "since": 0})
	items, _ := page["items"].([]any)
	if refused || len(items) != 1 {
		t.Fatalf("page=%v", page)
	}
	item := items[0].(map[string]any)
	if item["truncated"] != true || item["bodyRef"] != "body-long" || len(item["text"].(string)) > mcpItemTextLimit {
		t.Fatalf("item truncated=%v bodyRef=%v text=%d bytes", item["truncated"], item["bodyRef"], len(item["text"].(string)))
	}
	var rebuilt strings.Builder
	offset := 0
	for {
		chunk, refused := callMCP(t, session, "agenthail_read", map[string]any{"sessionId": from.ID, "bodyRef": "body-long", "offset": offset})
		if refused {
			t.Fatalf("chunk at %d: %v", offset, chunk)
		}
		rebuilt.WriteString(chunk["text"].(string))
		next, more := chunk["nextOffset"].(float64)
		if !more {
			break
		}
		offset = int(next)
	}
	if rebuilt.String() != full {
		t.Fatalf("rebuilt %d bytes, want %d", rebuilt.Len(), len(full))
	}
}
