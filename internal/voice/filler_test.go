package voice

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestOnlyFillerIsVoiceOnly(t *testing.T) {
	for text, want := range map[string]bool{
		"Um.":                       true,
		"Thank you!":                true,
		"uh, thanks":                true,
		"Hmm, got it, cool.":        true,
		"Yes.":                      false,
		"No":                        false,
		"Okay.":                     false,
		"Sure":                      false,
		"Um, okay, thanks.":         false,
		"Thanks, now run the tests": false,
		"um uh hmm thanks thanks":   false,
		"Run the reconnect tests":   false,
		"":                          false,
		"...":                       false,
	} {
		if got := voiceOnly(text); got != want {
			t.Errorf("voiceOnly(%q)=%v want %v", text, got, want)
		}
	}
}

func TestFillerStaysWithVoiceAndIsLoggedWhileRequestsReachTheSession(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Fieldnotes build", Transport: "desktop"}}
	f := newFixture(t, fixtureOptions{target: target})
	var mu sync.Mutex
	var logged []string
	f.service.SetLog(func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprintf(format, args...))
	})
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: target.session.ID})
	connectFixture(t, f)

	filler := Event{Sequence: 40, Method: "thread/realtime/transcript/done", Params: map[string]any{"role": "user", "text": "Thank you."}}
	f.observe(filler)
	f.observe(filler)
	f.observe(Event{Sequence: 41, Method: "thread/realtime/transcript/done", Params: map[string]any{"role": "user", "text": "Um, okay, thanks."}})
	waitFor(t, "the request to reach the session", func() bool { return len(target.sends()) == 1 })
	f.service.Close()
	if sends := target.sends(); !reflect.DeepEqual(sends, []string{"Um, okay, thanks."}) {
		t.Fatalf("sends=%q", sends)
	}
	held := 0
	for _, event := range f.View().Events {
		if event.Method == "voice/utterance/voice-only" {
			held++
			if event.Params["messageId"] != "voice-transcript:40" || event.Params["targetId"] != "target-a" || event.Params["text"] != "Thank you." {
				t.Fatalf("held event=%+v", event)
			}
		}
	}
	if held != 1 {
		t.Fatalf("held %d times, want once: %+v", held, f.View().Events)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 || logged[0] != `voice: kept "Thank you." with voice instead of sending it to Fieldnotes build (target-a, voice-transcript:40)` {
		t.Fatalf("logged=%q", logged)
	}
}

func TestMutedFillerIsHeldAsMuted(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Fieldnotes build", Transport: "desktop"}}
	f := targetSelected(t, target)
	muted := true
	apply(t, f, Action{Action: "input-state", AttemptID: "call-a", Muted: &muted, InputRevision: 1})
	f.observe(Event{Sequence: 50, Method: "thread/realtime/transcript/done", Params: map[string]any{"role": "user", "text": "Thanks."}})
	v := f.View()
	if !hasEventWith(v, "voice/delegation/held", "messageId", "voice-transcript:50") || hasEvent(v, "voice/utterance/voice-only") {
		t.Fatalf("events=%+v", v.Events)
	}
}
