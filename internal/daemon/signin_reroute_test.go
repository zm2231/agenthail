package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

type unreadableSignin struct{}

func (unreadableSignin) ResolveSignin(signin.Target) (signin.Source, error) {
	return signin.Source{}, signin.ProblemError{Consumer: signin.Claude, Err: errors.New("no sign-in profile is signed into claude.ai as the session's account")}
}

func (unreadableSignin) SigninCandidates(signin.Consumer) ([]signin.Source, error) {
	return nil, signin.ProblemError{Consumer: signin.Claude, Err: errors.New("no sign-in profile is signed into claude.ai as the session's account")}
}

func TestAPersonsMessageRemoteControlCannotSignInForIsDeliveredOnceOnTheSocket(t *testing.T) {
	f := newHumanTurnFixtureSignedIn(t, unreadableSignin{})
	f.d.discoverCatalog(t.Context())
	var atSend surface.PersonMessage
	f.claude.beforeSend = func() { atSend = storedPersonMessage(t, f, f.bridged.ID) }
	receipt := f.send(t, `{"action":"send","author":"person","sessionId":"conversation-bridge","message":"from the phone"}`)

	if receipt["evidence"] != string(surface.EvidenceTransportAccepted) || receipt["deliveredAs"] != "note" || len(f.posts) != 0 || len(f.claude.native) != 1 || f.claude.native[0] != "from the phone" {
		t.Fatalf("receipt=%v posts=%d native=%v, want nothing posted and the message accepted once on the socket", receipt, len(f.posts), f.claude.native)
	}
	if atSend != surface.PersonNote {
		t.Fatal("the row still promised the person's own turn when the message went out on the socket as a note")
	}
	history, err := f.r.ListHistory(10, f.bridged.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) < 2 || history[0].Kind != "transport-accepted" || history[0].SendRoute != "peer" || history[1].Kind != "rerouted" || history[1].SendRoute != "human-turn" {
		t.Fatalf("history=%+v, want the refused Remote Control attempt, then the socket delivery", history)
	}
	if history[0].SourceSessionID != surface.PersonSessionID || history[1].SourceSessionID != surface.PersonSessionID {
		t.Fatalf("history=%+v, want both entries written by the person", history)
	}
	f.d.syncDeliveryAsks()
	if asks := openDeliveryAsks(t, f.r); len(asks) != 0 {
		t.Fatalf("asks=%+v, want no delivery ask for a message the socket took", asks)
	}

	f.send(t, `{"action":"send","author":"person","sessionId":"conversation-bridge","message":"and again"}`)
	if len(f.claude.native) != 2 || f.claude.native[1] != "and again" || len(f.posts) != 0 {
		t.Fatalf("posts=%d native=%v, want the next message straight on the socket", len(f.posts), f.claude.native)
	}
	if history, err := f.r.ListHistory(1, f.bridged.ID); err != nil || len(history) != 1 || history[0].Kind != "transport-accepted" || history[0].SendRoute != "peer" {
		t.Fatalf("history=%+v err=%v, want the next message routed to the socket without another refused attempt", history, err)
	}
}

func TestAPersonsSteerRemoteControlCannotSignInForReachesTheRunningTurnOnTheSocket(t *testing.T) {
	f := newHumanTurnFixtureSignedIn(t, unreadableSignin{})
	f.transcript(t, f.bridged, idleClaudeTranscript+busyClaudeTranscript)
	receipt := f.send(t, `{"action":"steer","author":"person","sessionId":"conversation-bridge","message":"look at the logs first"}`)

	if receipt["evidence"] != string(surface.EvidenceTransportAccepted) || len(f.posts) != 0 || len(f.claude.native) != 1 || f.claude.native[0] != "look at the logs first" {
		t.Fatalf("receipt=%v posts=%d native=%v, want the steer accepted once on the socket", receipt, len(f.posts), f.claude.native)
	}
	f.d.syncDeliveryAsks()
	if asks := openDeliveryAsks(t, f.r); len(asks) != 0 {
		t.Fatalf("asks=%+v, want no delivery ask for a steer the socket took", asks)
	}
}

func TestAPersonsMessageRemoteControlRefusesWith401IsDeliveredOnceOnTheSocket(t *testing.T) {
	f := newHumanTurnFixture(t)
	f.postStatus = http.StatusUnauthorized
	receipt := f.send(t, `{"action":"send","author":"person","sessionId":"conversation-bridge","message":"from the phone"}`)

	if receipt["evidence"] != string(surface.EvidenceTransportAccepted) || receipt["deliveredAs"] != "note" || len(f.posts) != 1 || len(f.claude.native) != 1 || f.claude.native[0] != "from the phone" {
		t.Fatalf("receipt=%v posts=%d native=%v, want one refused post and the message accepted once on the socket", receipt, len(f.posts), f.claude.native)
	}
	f.d.syncDeliveryAsks()
	if asks := openDeliveryAsks(t, f.r); len(asks) != 0 {
		t.Fatalf("asks=%+v, want no delivery ask for a message the socket took", asks)
	}
}

func TestAProfileWhoseCookieWasRotatedAnswersOnFreshCookiesAndThePersonsTurnGoesOutOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	f := newHumanTurnFixture(t)
	login, _ := json.Marshal(map[string]any{"oauthAccount": map[string]any{"accountUuid": "acct-person", "emailAddress": "person@example.com", "organizationUuid": "org-person"}})
	if err := os.WriteFile(filepath.Join(f.process.Home, ".claude.json"), login, 0600); err != nil {
		t.Fatal(err)
	}
	f.bridged.Transcript = f.process.Transcript(f.bridged.ID, f.process.Home)
	if err := f.r.RecordObservedSession(f.bridged); err != nil {
		t.Fatal(err)
	}
	f.transcript(t, f.bridged, idleClaudeTranscript)
	probes := 0
	f.get = func(url string) (int, string, error) {
		if !strings.HasSuffix(url, "/api/account") {
			t.Fatalf("unexpected GET %s", url)
		}
		probes++
		if probes == 1 {
			return http.StatusUnauthorized, `{"error":{"type":"authentication_error"}}`, nil
		}
		return http.StatusOK, `{"uuid":"acct-person","email_address":"person@example.com"}`, nil
	}
	receipt := f.send(t, `{"action":"send","author":"person","sessionId":"conversation-bridge","message":"from the phone"}`)

	if receipt["evidence"] != string(surface.EvidenceDelivered) || receipt["deliveredAs"] != "turn" || len(f.posts) != 1 || len(f.claude.native) != 0 || probes != 2 {
		t.Fatalf("receipt=%v posts=%d native=%v probes=%d, want the account asked again and the turn posted once", receipt, len(f.posts), f.claude.native, probes)
	}
	assertHumanTurnPost(t, f.posts[0], "from the phone")
	f.d.syncDeliveryAsks()
	if asks := openDeliveryAsks(t, f.r); len(asks) != 0 {
		t.Fatalf("asks=%+v, want no delivery ask", asks)
	}
}
