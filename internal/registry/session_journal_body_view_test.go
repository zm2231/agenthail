package registry

import (
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestABodyViewKeepsItsStampWhileTheTailGrowsAndChangesItOnARewrite(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "view", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	retention := SessionJournalRetention{Count: 100, Bytes: 1 << 20}
	base := strings.Repeat("é", 10)
	entry := SessionJournalEntry{SessionID: session.ID, Kind: "text", ProviderKey: "reply", Payload: []byte(`{"role":"assistant"}`), BodyRef: "ref-1", FullBody: []byte(base)}
	if _, _, err := r.AppendSessionJournalEntry(entry, retention); err != nil {
		t.Fatal(err)
	}
	body := base
	for _, chunk := range []string{"añb", "c", "ü€d"} {
		if _, err := r.AppendSessionJournalBody(SessionJournalEntry{SessionID: session.ID, Kind: "text", ProviderKey: "reply", Payload: []byte(`{"role":"assistant"}`), BodyRef: "ref-1"}, len(body), []byte(chunk), retention); err != nil {
			t.Fatal(err)
		}
		body += chunk
	}
	first, found, err := r.SessionJournalBodyView(session.ID, "ref-1", 0, 0)
	if err != nil || !found || first.Total != len(body) || string(first.Tail) != body {
		t.Fatalf("whole view total=%d tail=%q found=%v err=%v, want %q", first.Total, first.Tail, found, err, body)
	}
	for lo := 0; lo <= len(body); lo++ {
		view, _, err := r.SessionJournalBodyView(session.ID, "ref-1", lo, lo)
		if err != nil || view.Stamp != first.Stamp || string(view.Head) != body[:lo] || string(view.Tail) != body[lo:] {
			t.Fatalf("at %d: head=%q tail=%q stamp=%d err=%v, want %q|%q stamp %d", lo, view.Head, view.Tail, view.Stamp, err, body[:lo], body[lo:], first.Stamp)
		}
	}
	entry.FullBody = []byte(body + "!")
	if _, _, err := r.AppendSessionJournalEntry(entry, retention); err != nil {
		t.Fatal(err)
	}
	rewritten, _, err := r.SessionJournalBodyView(session.ID, "ref-1", 0, len(body))
	if err != nil || rewritten.Stamp == first.Stamp || string(rewritten.Tail) != "!" {
		t.Fatalf("after a whole write stamp=%d (was %d) tail=%q err=%v", rewritten.Stamp, first.Stamp, rewritten.Tail, err)
	}
}
