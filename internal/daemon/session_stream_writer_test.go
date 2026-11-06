package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestALongLivedStreamRemembersABoundedNumberOfRepliesAndStillAppendsToALiveOne(t *testing.T) {
	var wire bytes.Buffer
	stream := newSessionStreamWriter(&wire, "s", func(row sessionJournalPayload) surface.TimelineItem {
		return journalTimelineItem(row)
	}, func(string, int, int) (registry.SessionJournalBodyView, bool, error) {
		return registry.SessionJournalBodyView{}, false, nil
	})
	seq := uint64(0)
	write := func(item, body string) string {
		t.Helper()
		seq++
		payload, err := json.Marshal(sessionJournalPayload{ItemID: item, Version: seq, Op: "upsert", Kind: "text", Role: "assistant", Body: body})
		if err != nil {
			t.Fatal(err)
		}
		wire.Reset()
		if err := stream.write(registry.SessionJournalEntry{SessionID: "s", Seq: seq, Payload: payload}); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.SplitN(wire.String(), "data: ", 2)[1])), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Type
	}
	live := "first"
	write("live", live)
	for index := 0; index <= sessionJournalRetentionCount; index++ {
		write(fmt.Sprintf("reply-%d", index), "done")
		if index%(heldReplyLimit/2) == 0 {
			live += fmt.Sprintf(" %d", index)
			if got := write("live", live); got != "append" {
				t.Fatalf("a reply that keeps growing was sent %q after %d other replies", got, index)
			}
		}
		if len(stream.held) > heldReplyLimit {
			t.Fatalf("the connection holds %d replies, past its limit of %d", len(stream.held), heldReplyLimit)
		}
	}
	if got := write("reply-0", "done, then more"); got != "item" {
		t.Fatalf("a reply the connection let go grew and was sent %q, want it whole", got)
	}
}
