package surface

import (
	"testing"
	"time"
)

func TestASentAgeReadsInTheLargestUnitThatFits(t *testing.T) {
	now := time.Now()
	for waited, want := range map[time.Duration]string{
		9 * time.Minute:  "hi",
		90 * time.Minute: "(sent 90 min ago) hi",
		5 * time.Hour:    "(sent 5 h ago) hi",
		72 * time.Hour:   "(sent 3 days ago) hi",
	} {
		if got := WithSentAge("hi", now.Add(-waited), now); got != want {
			t.Fatalf("after %s: %q, want %q", waited, got, want)
		}
	}
	if got := WithSentAge("[from @lead via #widget] hi", now.Add(-time.Hour), now); got != "[from @lead via #widget] (sent 60 min ago) hi" {
		t.Fatalf("envelope: %q", got)
	}
	if envelope, ok := ParsePeerEnvelope(WithSentAge("[from @lead] hi", now.Add(-time.Hour), now)); !ok || envelope.Sender != "@lead" || envelope.Body != "(sent 60 min ago) hi" {
		t.Fatalf("parsed %+v ok=%v", envelope, ok)
	}
}
