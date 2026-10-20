package surfaces

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const claudeCatchUpTurns = `{"type":"assistant","uuid":"a1","timestamp":"2026-10-19T10:00:01Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"one"}]}}
{"type":"user","uuid":"u2","timestamp":"2026-10-19T10:01:00Z","message":{"content":"second"}}
{"type":"assistant","uuid":"a2","timestamp":"2026-10-19T10:01:01Z","message":{"id":"m2","content":[{"type":"text","text":"working"}]}}
{"type":"attachment","uuid":"q1","timestamp":"2026-10-19T10:01:02Z","attachment":{"type":"queued_command","prompt":"<cross-session-message from=\"uds:/tmp/s.sock\" from-name=\"reviewer\">steer</cross-session-message>","origin":{"kind":"peer","name":"reviewer","msg_id":"msg-1","body":"steer"}}}
{"type":"assistant","uuid":"a2b","timestamp":"2026-10-19T10:01:03Z","message":{"id":"m3","stop_reason":"end_turn","content":[{"type":"text","text":"two"}]}}
{"type":"system","subtype":"turn_duration","timestamp":"2026-10-19T10:01:04Z","durationMs":4000}
{"type":"user","uuid":"u3","timestamp":"2026-10-19T10:02:00Z","origin":{"kind":"task-notification"},"message":{"content":"` + syntheticNotification + `"}}
{"type":"assistant","uuid":"a3","timestamp":"2026-10-19T10:02:01Z","message":{"id":"m4","stop_reason":"end_turn","content":[{"type":"text","text":"three"}]}}
{"type":"user","uuid":"u4","timestamp":"2026-10-19T10:03:00Z","message":{"content":"fourth"}}
{"type":"assistant","uuid":"a4","timestamp":"2026-10-19T10:03:01Z","message":{"id":"m5","content":[{"type":"text","text":"starting"}]}}
{"type":"user","uuid":"i4","timestamp":"2026-10-19T10:03:02Z","message":{"content":"[Request interrupted by user]"}}
{"type":"user","uuid":"u5","timestamp":"2026-10-19T10:04:00Z","message":{"content":"fifth"}}
{"type":"assistant","uuid":"a5","timestamp":"2026-10-19T10:04:01Z","message":{"id":"m6","stop_reason":"end_turn","content":[{"type":"text","text":"five"}]}}
`

func TestClaudeCatchUpPageCarriesTheLiveTurnsAndDoneKeys(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"u1","timestamp":"2026-10-19T10:00:00Z","message":{"content":"first"}}`)
	session := seededClaudeStreamSession(t, path, "")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(claudeCatchUpTurns); err != nil {
		t.Fatal(err)
	}
	file.Close()
	live := map[string]string{}
	liveDone := map[string]string{}
	err = NewClaude(nil, t.TempDir()).Stream(context.Background(), session, "", func(event surface.StreamEvent) {
		if event.Kind == "done" {
			liveDone[event.ID] = event.TurnID
		} else if event.ID != "" {
			live[event.ID] = event.TurnID
		}
	}, 150*time.Millisecond)
	if !errors.Is(err, surface.ErrStreamWindow) {
		t.Fatalf("stream err=%v", err)
	}
	page, err := readTimeline(context.Background(), path, "claude", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	paged := map[string]string{}
	pageDone := map[string]string{}
	for _, item := range page.Items {
		if item.Kind == "done" {
			pageDone[item.ID] = item.TurnID
		} else if _, streamed := live[item.ID]; streamed {
			paged[item.ID] = item.TurnID
		}
	}
	if !reflect.DeepEqual(paged, live) {
		t.Fatalf("page turns %v, live turns %v", paged, live)
	}
	if !reflect.DeepEqual(pageDone, liveDone) || len(liveDone) != 4 {
		t.Fatalf("page done %v, live done %v", pageDone, liveDone)
	}
	exchanges := page.Exchanges
	if len(exchanges) != 5 {
		t.Fatalf("exchanges=%+v", exchanges)
	}
	if exchanges[1].User != "second" || exchanges[1].Assistant != "two" {
		t.Fatalf("a mid-turn steer split its turn: %+v", exchanges[1])
	}
	if exchanges[2].Origin != "task-notification" || exchanges[2].Assistant != "three" {
		t.Fatalf("task notification exchange %+v", exchanges[2])
	}
	for index, exchange := range exchanges {
		if exchange.User == "" || exchange.Timestamp.IsZero() {
			t.Fatalf("exchange %d has no prompt or time: %+v", index, exchange)
		}
	}
}

func TestClaudeTranscriptItemsMatchThePageFromAnOffset(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"u1","timestamp":"2026-10-19T10:00:00Z","message":{"content":"first"}}`+"\n"+claudeCatchUpTurns)
	page, err := readTimeline(context.Background(), path, "claude", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	from := transcriptItemOffsetForTest(t, page.Items[3].ID)
	got, err := NewClaude(nil, t.TempDir()).TranscriptItems(context.Background(), &surface.Session{Transcript: path, Status: surface.StatusIdle}, from)
	if err != nil {
		t.Fatal(err)
	}
	// The record at from also carries the done of the turn closing before it.
	want := page.Items[2:]
	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d", len(got), len(want))
	}
	for index := range got {
		if got[index].ID != want[index].ID || got[index].TurnID != want[index].TurnID || got[index].Kind != want[index].Kind || got[index].Text != want[index].Text {
			t.Fatalf("item %d: got %+v want %+v", index, got[index], want[index])
		}
	}
}

func transcriptItemOffsetForTest(t *testing.T, id string) int64 {
	t.Helper()
	var offset int64
	for _, digit := range id {
		if digit < '0' || digit > '9' {
			break
		}
		offset = offset*10 + int64(digit-'0')
	}
	if id == "" || id[0] < '0' || id[0] > '9' {
		t.Fatalf("item %q carries no offset", id)
	}
	return offset
}
