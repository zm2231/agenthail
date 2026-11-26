package daemon

import (
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func codexInput(id, text, messageID, origin string, at time.Time) surface.StreamEvent {
	return surface.StreamEvent{ID: id, ProviderKey: id, Operation: "upsert", Final: true, Kind: "message", Role: "user", Text: text, MessageID: messageID, Origin: origin, Timestamp: at}
}

func TestPromptsListThePersonsWordsInAnAgentsSessionAndLeaveOutWhatAgentsSent(t *testing.T) {
	d, r, fake, person, worker := daemonFixture(t)
	if err := r.RecordSessionOrigin(registry.OriginOf(worker.ID, person.ID)); err != nil {
		t.Fatal(err)
	}
	for id, sender := range map[string]string{"brief": person.ID, "rebase": person.ID, "typed-in-app": surface.PersonSessionID, "coordinator": surface.SystemSessionID} {
		if err := r.RecordMessageSender(id, sender); err != nil {
			t.Fatal(err)
		}
	}
	since, err := r.MessageSendersSince(worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	at := since.Add(time.Minute)
	workerSource, personSource := journalSource(d, worker, fake), journalSource(d, person, fake)

	workerSource.append(codexInput("w1", "Fix the parser", "brief", "", at))
	workerSource.append(codexInput("w2", "Rename the flag", "", "", at.Add(time.Second)))
	workerSource.append(codexInput("w2", "Rename the flag", "typed-in-app", "", at.Add(time.Second)))
	workerSource.append(codexInput("w3", "[from @lead] Rebase onto main", "rebase", "", at.Add(2*time.Second)))
	workerSource.append(codexInput("w4", "Also bump the version", "", surface.OriginPerson, at.Add(3*time.Second)))
	workerSource.append(codexInput("w5", "Plan the release", "coordinator", "", at.Add(4*time.Second)))
	workerSource.append(codexInput("w6", "An old brief", "", surface.OriginPerson, since.Add(-time.Hour)))
	personSource.append(codexInput("p1", "An old prompt of mine", "", surface.OriginPerson, since.Add(-time.Hour)))
	personSource.append(codexInput("p2", "Run the linter", "rebase", "", since.Add(-time.Hour)))

	code, body, raw := getPrompts(t, dashboardRouter(d), "")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	if got, want := promptTexts(body.Prompts), "Also bump the version|Rename the flag|An old prompt of mine"; got != want {
		t.Fatalf("prompts = %q, want %q", got, want)
	}
	code, body, raw = getPrompts(t, dashboardRouter(d), "sessionId="+worker.ID)
	if code != http.StatusOK || promptTexts(body.Prompts) != "Also bump the version|Rename the flag" {
		t.Fatalf("the agent's session %d %s", code, raw)
	}
}

func TestAMovedCodexSessionKeepsWhatAgentsSentItElsewhereOutOfThePrompts(t *testing.T) {
	d, r, fake, lead, worker := daemonFixture(t)
	if err := r.RecordSessionOrigin(registry.OriginOf(worker.ID, lead.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PrepareMoveIn("move-1", worker.ID, "h00000000000a", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-1", registry.MoveReceived, "", registry.MovePrepared); err != nil {
		t.Fatal(err)
	}
	sourceClock := time.Now().Add(time.Hour)
	carry := registry.MoveCarry{SendersUnknownBefore: sourceClock.UnixMilli()}
	if _, err := r.TakeMovedSession("move-1", worker, "", time.Time{}, nil, carry, "", registry.MovedCoordinatorPlace{}); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordMessageSender("sent-here", lead.ID); err != nil {
		t.Fatal(err)
	}
	source := journalSource(d, worker, fake)
	source.append(codexInput("w1", "Rebase onto main", "sent-on-the-other-host", "", sourceClock.Add(-time.Second)))
	source.append(codexInput("w2", "Rename the flag", "", surface.OriginPerson, sourceClock.Add(-time.Second)))
	source.append(codexInput("w3", "Tag the release", "sent-here", "", sourceClock.Add(time.Minute)))
	source.append(codexInput("w4", "Also bump the version", "", surface.OriginPerson, sourceClock.Add(2*time.Minute)))

	code, body, raw := getPrompts(t, dashboardRouter(d), "sessionId="+worker.ID)
	if code != http.StatusOK || promptTexts(body.Prompts) != "Also bump the version" {
		t.Fatalf("%d %s", code, raw)
	}
}
