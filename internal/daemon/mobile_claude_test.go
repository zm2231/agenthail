package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestMobileClaudeCreationOpensTheSessionInTheHostAndRegistersIt(t *testing.T) {
	for _, launcher := range []string{"", surface.LauncherClaudeSDK} {
		t.Run("launcher="+launcher, func(t *testing.T) { createClaudeSessionInTheHost(t, launcher) })
	}
}

// createClaudeSessionInTheHost creates a session through the default launcher
// or the one named, which must carry every creation option to the host.
func createClaudeSessionInTheHost(t *testing.T, launcher string) {
	_, registry, _, _, _ := daemonFixture(t)
	host := claudehosttest.New(t, "")
	host.Start(t)
	home := host.Cwd
	d := New(registry, []surface.Surface{surfaces.NewClaude("", host.Home)})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session-options", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var options struct {
		Launchers []struct {
			ID             string `json:"id"`
			SessionOptions bool   `json:"sessionOptions"`
		} `json:"launchers"`
	}
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"claude"`) || json.Unmarshal(response.Body.Bytes(), &options) != nil {
		t.Fatal(response.Body.String())
	}
	for _, launcher := range options.Launchers {
		if want := launcher.ID == surface.LauncherClaudeSDK || launcher.ID == surface.LauncherClaudeBG; launcher.SessionOptions != want {
			t.Fatalf("launcher %s sessionOptions=%v, want %v", launcher.ID, launcher.SessionOptions, want)
		}
	}
	body, _ := json.Marshal(map[string]string{"action": "session-create", "surface": "claude", "message": "--literal instruction", "cwd": home, "model": "sonnet", "name": "phone-task", "worktree": "phone-work", "agent": "reviewer", "effort": "high", "permissionMode": "plan", "launcher": launcher})
	request = httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var receipt struct {
		OK      bool                `json:"ok"`
		Session surface.Session     `json:"session"`
		Result  *surface.SendResult `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(response.Body.String(), err)
	}
	if response.Code != 201 || !receipt.OK || receipt.Session.RuntimeKind != surface.RuntimeClaudeHost || receipt.Session.Transport != "uds" || receipt.Result == nil || !receipt.Result.Accepted {
		t.Fatal(response.Body.String())
	}
	if stored, err := registry.Session(receipt.Session.ID); err != nil || stored.RuntimeKind != surface.RuntimeClaudeHost || stored.Transport != "uds" || stored.Runtime == nil || stored.Runtime.Launcher != surface.LauncherClaudeSDK {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	spawns := host.Spawns(t, receipt.Session.ID)
	if len(spawns) != 1 {
		t.Fatalf("spawns=%+v", spawns)
	}
	argv := strings.Join(spawns[0].Argv, " ")
	for _, want := range []string{"sonnet", "phone-task", "phone-work", "reviewer", "high", "plan"} {
		if !strings.Contains(argv, want) {
			t.Fatalf("missing %q in %s", want, argv)
		}
	}
	claudehosttest.WaitFor(t, "the first turn", func() bool {
		turns := host.Turns(t, receipt.Session.ID)
		return len(turns) == 1 && turns[0].Text == "--literal instruction" && turns[0].UUID == receipt.Result.UUID
	})
}

// lostAnswerClaude starts sessions in the host and then loses the answer to
// the first turn, as a dropped connection after the host accepted it would.
type lostAnswerClaude struct{ *surfaces.Claude }

func (c lostAnswerClaude) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	session, _, err := c.Claude.StartSession(ctx, options)
	if err != nil {
		return nil, nil, err
	}
	return session, nil, surface.DeliveryOutcomeUnknown(errors.New("connection lost before the host answered"))
}

func TestAClaudeCreationWhoseFirstTurnIsUnconfirmedRecordsThatTurnsUUID(t *testing.T) {
	_, registry, _, _, _ := daemonFixture(t)
	host := claudehosttest.New(t, "")
	host.Start(t)
	d := New(registry, []surface.Surface{lostAnswerClaude{surfaces.NewClaude("", host.Home)}})
	body, _ := json.Marshal(map[string]string{"action": "session-create", "surface": "claude", "message": "first", "cwd": host.Cwd})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	d.dashboardHandler(&dashboardServer{token: "secret"}).ServeHTTP(response, request)
	var receipt struct {
		Status     string          `json:"status"`
		DeliveryID int64           `json:"deliveryId"`
		Session    surface.Session `json:"session"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil || receipt.Status != "submitted" || receipt.DeliveryID == 0 {
		t.Fatalf("status=%d body=%s err=%v", response.Code, response.Body.String(), err)
	}
	intent, err := registry.DeliveryIntent(receipt.DeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	claudehosttest.WaitFor(t, "the first turn", func() bool { return len(host.Turns(t, receipt.Session.ID)) == 1 })
	time.Sleep(300 * time.Millisecond)
	turns := host.Turns(t, receipt.Session.ID)
	if len(turns) != 1 || turns[0].UUID != intent.UUID {
		t.Fatalf("turns=%+v intent=%s, want one turn under the intent's uuid", turns, intent.UUID)
	}
}

func TestMobileQueuePreservesIntegratedTurnSettings(t *testing.T) {
	d, registry, _, from, _ := daemonFixture(t)
	_, err := registry.QueueMessageWithOptions(from.ID, "queued", "", surface.SendOptions{TurnOptions: surface.TurnOptions{Effort: "high", Mode: "plan", ServiceTier: "fast", OutputSchema: json.RawMessage(`{"type":"object"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/queue", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	d.dashboardHandler(&dashboardServer{token: "secret"}).ServeHTTP(response, request)
	for _, want := range []string{`"effort":"high"`, `"mode":"plan"`, `"serviceTier":"fast"`, `"outputSchema":{"type":"object"}`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatal(response.Body.String())
		}
	}
}
