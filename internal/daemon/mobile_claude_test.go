package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
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
	d := newTestDaemon(t, registry, []surface.Surface{surfaces.NewClaude(nil, host.Home)})
	handler := dashboardRouter(d)
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
	testkit.Eventually(t, "the first turn", 15*time.Second, func() bool {
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
	d := newTestDaemon(t, registry, []surface.Surface{lostAnswerClaude{surfaces.NewClaude(nil, host.Home)}})
	body, _ := json.Marshal(map[string]string{"action": "session-create", "surface": "claude", "message": "first", "cwd": host.Cwd})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	dashboardRouter(d).ServeHTTP(response, request)
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
	testkit.Eventually(t, "the first turn", 15*time.Second, func() bool { return len(host.Turns(t, receipt.Session.ID)) == 1 })
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
	dashboardRouter(d).ServeHTTP(response, request)
	for _, want := range []string{`"effort":"high"`, `"mode":"plan"`, `"serviceTier":"fast"`, `"outputSchema":{"type":"object"}`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatal(response.Body.String())
		}
	}
}

func TestClaudeCreationThatNeverStartedIsAFailureNotUnknown(t *testing.T) {
	for _, test := range []struct {
		name   string
		binary func(home string) string
		want   string
	}{
		{name: "untrusted workspace", want: "Workspace not trusted", binary: func(home string) string {
			executable := filepath.Join(home, "claude")
			script := "#!/bin/sh\nprintf 'Workspace not trusted. Run `claude` in %s once and accept the trust prompt, then retry.\\n' \"$PWD\" >&2\nexit 1\n"
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			return executable
		}},
		{name: "missing executable", want: "is not executable", binary: func(home string) string { return filepath.Join(home, "missing-claude") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, registry, _, _, _ := daemonFixture(t)
			home := t.TempDir()
			t.Setenv("AGENTHAIL_CLAUDE_BIN", test.binary(home))
			d := newTestDaemon(t, registry, []surface.Surface{surfaces.NewClaude(nil, home)})
			body, _ := json.Marshal(map[string]string{"action": "session-create", "surface": "claude", "launcher": surface.LauncherClaudeBG, "message": "fixture instruction", "cwd": home})
			response := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", string(body))
			var failure struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if response.Code != http.StatusBadGateway || json.Unmarshal(response.Body.Bytes(), &failure) != nil || failure.Error.Code != "create_failed" || strings.Count(failure.Error.Message, "delivery did not start") != 1 || !strings.Contains(failure.Error.Message, test.want) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			history, err := registry.ListHistory(10, "")
			if err != nil || len(history) != 1 || history[0].Kind != "failed" || history[0].Message != "fixture instruction" || history[0].Error != failure.Error.Message {
				t.Fatalf("history=%+v err=%v", history, err)
			}
		})
	}
}
