package voice

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/surface"
)

type fixedBriefer struct{ src BriefSource }

func (b fixedBriefer) BriefSource(_ context.Context, session *surface.Session) BriefSource {
	src := b.src
	src.Session = *session
	return src
}

func briefTime(minute int) time.Time { return time.Date(2026, 10, 23, 12, minute, 0, 0, time.UTC) }

func TestABriefKeepsTheNewestExchangesWithinItsCapAndNamesWhatItLeftOut(t *testing.T) {
	var exchanges []surface.Exchange
	for i := range 40 {
		exchanges = append(exchanges, surface.Exchange{User: fmt.Sprintf("request %d %s", i, strings.Repeat("détail ", 30)), Assistant: fmt.Sprintf("reply %d %s", i, strings.Repeat("résultat ", 150)), Timestamp: briefTime(i)})
	}
	compactions := 2
	text, info := RenderBrief(BriefSource{
		Session:     surface.Session{ID: "target-a", Name: "Fieldnotes build", Surface: surface.KindCodex, Status: surface.StatusBusy},
		Goal:        &surface.GoalState{Objective: "Make the reconnect build reliable", Status: "active"},
		Compactions: &compactions,
		Exchanges:   exchanges,
		Missing:     []string{"goal budget: " + strings.Repeat("x", 500)},
		AsOf:        briefTime(59),
	})
	if len(text) > briefBytes || info.Bytes != len(text) {
		t.Fatalf("brief is %d bytes (info %d), cap %d", len(text), info.Bytes, briefBytes)
	}
	if !strings.Contains(text, "request 39") || strings.Index(text, "request 39") > strings.Index(text, "request 38") {
		t.Fatalf("newest exchange is not first:\n%s", text)
	}
	if strings.Contains(text, "request 0 ") {
		t.Fatalf("an old exchange survived the cap:\n%s", text)
	}
	for _, want := range []string{"Status: working on a turn now.", "Goal (active): Make the reconnect build reliable", "Context compactions so far: 2.", "say you don't know", fmt.Sprintf("%d earlier exchange(s) left out for length", 40-info.Exchanges), "of the exchanges shown were shortened", "goal budget: xxx"} {
		if !strings.Contains(text, want) {
			t.Fatalf("brief lacks %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "Not included:") || len(info.Omitted) < 3 {
		t.Fatalf("omitted parts were not named: %+v", info.Omitted)
	}
	if !strings.ContainsRune(text, '…') || strings.ContainsRune(text, '\uFFFD') {
		t.Fatalf("clipping broke a character:\n%s", text)
	}
}

func TestEveryPartOfABriefIsBoundedAndEveryShortenedPartIsNamed(t *testing.T) {
	var exchanges []surface.Exchange
	for i := range 40 {
		exchanges = append(exchanges, surface.Exchange{User: strings.Repeat("é", 2000), Assistant: strings.Repeat("ü", 4000), Timestamp: briefTime(i)})
	}
	var missing []string
	for i := range 60 {
		missing = append(missing, fmt.Sprintf("part %d: %s", i, strings.Repeat("why ", 200)))
	}
	text, info := RenderBrief(BriefSource{
		Session:   surface.Session{ID: strings.Repeat("i", 9000), Name: strings.Repeat("\x01名", 4000), Surface: surface.SurfaceKind(strings.Repeat("s", 900)), Status: surface.SessionStatus(strings.Repeat("z", 900))},
		Goal:      &surface.GoalState{Objective: strings.Repeat("goal ", 2000), Status: strings.Repeat("g", 900)},
		Exchanges: exchanges,
		Missing:   missing,
		AsOf:      briefTime(59),
	})
	if len(text) > briefBytes || info.Bytes != len(text) || !utf8.ValidString(text) {
		t.Fatalf("brief is %d bytes (info %d, valid %v), cap %d", len(text), info.Bytes, utf8.ValidString(text), briefBytes)
	}
	for _, want := range []string{"the session name was shortened", "the session id was shortened", "the session's provider was shortened", "the status was shortened", "the goal status was shortened", "the goal was shortened", "earlier exchange(s) left out for length", "more."} {
		if !strings.Contains(text, want) {
			t.Fatalf("brief lacks %q:\n%s", want, text)
		}
	}
	if len(info.Omitted) != 6+2+60 || info.Exchanges == 0 {
		t.Fatalf("info=%d omitted, %d exchanges", len(info.Omitted), info.Exchanges)
	}
}

func TestABriefWithNothingReadSaysSo(t *testing.T) {
	text, info := RenderBrief(BriefSource{Session: surface.Session{ID: "target-a", Name: "Quiet", Surface: surface.KindClaude, Status: surface.StatusIdle}, Missing: []string{"recent exchanges: journal unavailable"}, AsOf: briefTime(0)})
	for _, want := range []string{"Status: idle.", "Recent exchanges: none supplied.", "Not included: recent exchanges: journal unavailable."} {
		if !strings.Contains(text, want) {
			t.Fatalf("brief lacks %q:\n%s", want, text)
		}
	}
	if info.Exchanges != 0 || len(info.Omitted) != 1 {
		t.Fatalf("info=%+v", info)
	}
}

func briefedTarget() *targetFixture {
	return &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Fieldnotes build", Transport: "desktop", Status: surface.StatusIdle}}
}

var sampleBrief = BriefSource{Exchanges: []surface.Exchange{{User: "Why does the build fail after reconnecting?", Assistant: "The reconnect drops the pending build state.", Timestamp: briefTime(1)}}, JournalSeq: 42, AsOf: briefTime(2)}

func TestASessionBoundCallStartsWithItsSessionsBrief(t *testing.T) {
	target := briefedTarget()
	f := newFixture(t, fixtureOptions{target: target})
	f.service.SetBriefer(fixedBriefer{src: sampleBrief})
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: target.session.ID})
	connectFixture(t, f)
	start := requestParams(f.provider, "thread/realtime/start")
	if len(start) != 1 {
		t.Fatalf("starts=%d", len(start))
	}
	items, _ := start[0]["initialItems"].([]map[string]any)
	if len(items) != 1 || items[0]["role"] != "developer" || !strings.Contains(items[0]["text"].(string), "The reconnect drops the pending build state.") || !strings.Contains(items[0]["text"].(string), `"Fieldnotes build"`) {
		t.Fatalf("start items=%v", start[0]["initialItems"])
	}
	if start[0]["prompt"] != sessionBoundPrompt || start[0]["includeStartupContext"] != false {
		t.Fatalf("start lost the session-bound prompt: %v", start[0])
	}
	if b := f.View().Brief; b == nil || b.SessionID != "target-a" || b.Exchanges != 1 || b.JournalSeq != 42 || b.Bytes == 0 {
		t.Fatalf("brief info=%+v", b)
	}
}

func TestAnOrchestratorCallHasNoSessionBrief(t *testing.T) {
	f := newFixture(t, fixtureOptions{target: briefedTarget()})
	f.service.SetBriefer(fixedBriefer{src: sampleBrief})
	connectFixture(t, f)
	start := requestParams(f.provider, "thread/realtime/start")
	items, _ := start[0]["initialItems"].([]map[string]any)
	if len(items) != 1 || strings.Contains(items[0]["text"].(string), "Context for the session") || f.View().Brief != nil {
		t.Fatalf("orchestrator call got a session brief: %v %+v", start[0]["initialItems"], f.View().Brief)
	}
}

func TestATransferCarriesTheBriefAndReturningClearsIt(t *testing.T) {
	target := briefedTarget()
	f := newFixture(t, fixtureOptions{target: target})
	f.service.SetBriefer(fixedBriefer{src: sampleBrief})
	connectFixture(t, f)
	apply(t, f, Action{Action: "transfer", TargetID: target.session.ID})
	notes := requestParams(f.provider, "thread/realtime/appendText")
	if len(notes) != 1 || !strings.HasPrefix(notes[0]["text"].(string), "Voice has transferred to the selected Agenthail session.") || !strings.Contains(notes[0]["text"].(string), "The reconnect drops the pending build state.") {
		t.Fatalf("transfer note=%v", notes)
	}
	if b := f.View().Brief; b == nil || b.SessionID != "target-a" {
		t.Fatalf("brief info=%+v", b)
	}
	apply(t, f, Action{Action: "transfer", TargetID: ""})
	if b := f.View().Brief; b != nil {
		t.Fatalf("return kept the brief: %+v", b)
	}
}

func TestACallWhoseBriefCouldNotBeReadStillStartsAndSaysWhatIsMissing(t *testing.T) {
	target := briefedTarget()
	f := newFixture(t, fixtureOptions{target: target})
	f.service.SetBriefer(fixedBriefer{src: BriefSource{Missing: []string{"recent exchanges: journal unavailable"}, AsOf: briefTime(3)}})
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: target.session.ID})
	connectFixture(t, f)
	items, _ := requestParams(f.provider, "thread/realtime/start")[0]["initialItems"].([]map[string]any)
	if len(items) != 1 || !strings.Contains(items[0]["text"].(string), "Not included: recent exchanges: journal unavailable.") {
		t.Fatalf("start items=%v", items)
	}
	if v := f.View(); v.Phase != "connected" || v.Brief == nil || len(v.Brief.Omitted) != 1 {
		t.Fatalf("view=%+v", v)
	}
}

func requestParams(p *fixtureProvider, method string) []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []map[string]any
	for i, m := range p.requests {
		if m == method {
			out = append(out, p.params[i])
		}
	}
	return out
}
