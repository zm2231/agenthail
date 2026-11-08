package surfaces

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAPageAndTheStreamPlaceASlashCommandLongerThanAMegabyteInTheSameTurn(t *testing.T) {
	command := `<command-name>/review</command-name><command-args>` + strings.Repeat("x", 2*compactLineBytes) + `</command-args>`
	head := `{"type":"user","uuid":"first","timestamp":"2026-10-19T10:00:00Z","message":{"content":"inspect"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-10-19T10:00:01Z","message":{"id":"m1","content":[{"type":"text","text":"looking"}]}}
`
	tail := `{"type":"user","uuid":"command","timestamp":"2026-10-19T10:00:02Z","message":{"content":` + strconv.Quote(command) + `}}
{"type":"assistant","uuid":"a2","timestamp":"2026-10-19T10:00:03Z","message":{"id":"m2","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}
{"type":"system","subtype":"turn_duration","timestamp":"2026-10-19T10:00:04Z","durationMs":4000}
`
	path := writeTranscript(t, head)
	session := seededClaudeStreamSession(t, path, "")
	testkit.AppendFile(t, path, tail)
	streamed := map[string]string{}
	err := NewClaude(nil, t.TempDir()).Stream(context.Background(), session, "", func(event surface.StreamEvent) {
		if event.ID != "" && event.ID[0] >= '0' && event.ID[0] <= '9' {
			streamed[event.ID] = event.Kind + " " + event.TurnID
		}
	}, 150*time.Millisecond)
	if !errors.Is(err, surface.ErrStreamWindow) {
		t.Fatal(err)
	}
	page, err := timelineClaude().ReadSession(context.Background(), &surface.Session{Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	paged := map[string]string{}
	for _, item := range page.Items {
		if transcriptItemOffsetForTest(t, item.ID) >= int64(len(head)) {
			paged[item.ID] = item.Kind + " " + item.TurnID
		}
	}
	if len(paged) == 0 || !reflect.DeepEqual(paged, streamed) {
		t.Fatalf("the page shows %v; the stream shows %v", paged, streamed)
	}
}
