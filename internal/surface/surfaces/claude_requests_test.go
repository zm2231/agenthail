package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
)

// openRequest starts a turn whose text makes the fake Claude ask the host,
// and waits until the session shows the request as what it waits on.
func openRequest(t *testing.T, c *Claude, fixture *claudehosttest.Fixture, text string) (*surface.Session, surface.Request) {
	t.Helper()
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	ctx := context.Background()
	if _, err := c.SendHumanTurn(surface.WithMessageUUID(ctx, uuid.NewString()), sess, text, nil); err != nil {
		t.Fatal(err)
	}
	var state surface.RequestState
	claudehosttest.WaitFor(t, "the open request", func() bool {
		var err error
		state, err = c.Requests(ctx, sess)
		if err != nil {
			t.Fatal(err)
		}
		return len(state.Requests) == 1
	})
	return sess, state.Requests[0]
}

func TestAHostRequestIsWhatTheSessionWaitsOnUntilItIsAnswered(t *testing.T) {
	c, fixture := hostedClaude(t)
	sess, request := openRequest(t, c, fixture, "TOOL")
	if request.Kind != surface.WaitingApproval || request.Tool != "Bash" || request.Title != "Claude wants to run true" || string(request.Input) != `{"command":"true"}` || request.AlwaysAllow {
		t.Fatalf("request = %+v", request)
	}
	ctx := context.Background()
	c.host.forget()
	observation, err := c.Observe(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Status != surface.StatusBusy || observation.WaitingOn == nil || observation.WaitingOn.ID != request.ID || observation.WaitingOn.Kind != surface.WaitingApproval {
		t.Fatalf("observation = %+v, want busy waiting on request %s", observation, request.ID)
	}
	outcome, err := c.Respond(ctx, sess, request.ID, surface.Decision{Behavior: "allow"})
	if err != nil || outcome != "allowed" {
		t.Fatalf("respond = %q, %v", outcome, err)
	}
	if _, err := c.Respond(ctx, sess, request.ID, surface.Decision{Behavior: "allow"}); !errors.Is(err, surface.ErrRequestStale) {
		t.Fatalf("second respond = %v, want stale", err)
	}
	claudehosttest.WaitFor(t, "the allowed turn", func() bool { return len(fixture.Turns(t, sess.ID)) == 1 })
	if reply := fixture.Turns(t, sess.ID)[0].Reply; reply != "tool:allow:" {
		t.Fatalf("reply = %q", reply)
	}
	c.host.forget()
	observation, err = c.Observe(ctx, sess)
	if err != nil || observation.WaitingOn != nil {
		t.Fatalf("observation after the answer = %+v, %v", observation, err)
	}
}

func TestAHostQuestionIsAnsweredWithItsOptionsAndAWrongSessionIsStale(t *testing.T) {
	c, fixture := hostedClaude(t)
	sess, request := openRequest(t, c, fixture, "QUESTION")
	if request.Kind != surface.WaitingQuestion || len(request.Questions) != 1 || request.Questions[0].Question != "Which color?" || len(request.Questions[0].Options) != 2 {
		t.Fatalf("request = %+v", request)
	}
	ctx := context.Background()
	other := *sess
	other.ID = uuid.NewString()
	if _, err := c.Respond(ctx, &other, request.ID, surface.Decision{Behavior: "allow", Answers: map[string]string{"Which color?": "Red"}}); !errors.Is(err, surface.ErrRequestStale) {
		t.Fatalf("respond for another session = %v, want stale", err)
	}
	if _, err := c.Respond(ctx, sess, request.ID, surface.Decision{Behavior: "allow"}); err == nil || errors.Is(err, surface.ErrRequestStale) {
		t.Fatalf("respond without answers = %v, want invalid", err)
	}
	outcome, err := c.Respond(ctx, sess, request.ID, surface.Decision{Behavior: "allow", Answers: map[string]string{"Which color?": "Blue"}})
	if err != nil || outcome != "answered" {
		t.Fatalf("respond = %q, %v", outcome, err)
	}
	claudehosttest.WaitFor(t, "the answered turn", func() bool { return len(fixture.Turns(t, sess.ID)) == 1 })
	if reply := fixture.Turns(t, sess.ID)[0].Reply; reply != `question:allow:{"Which color?":"Blue"}` {
		t.Fatalf("reply = %q", reply)
	}
}

func TestAHostSessionChangesModelAndPermissionModeLive(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	ctx := context.Background()
	if _, err := c.Control(ctx, sess, surface.ControlRequest{Action: "permission-mode", Value: "yolo"}); err == nil {
		t.Fatal("an unknown permission mode was accepted")
	}
	result, err := c.Control(ctx, sess, surface.ControlRequest{Action: "permission-mode", Value: "bypassPermissions"})
	if err != nil || result.Output != "bypassPermissions" || result.Evidence != surface.EvidenceDelivered {
		t.Fatalf("permission-mode = %+v, %v", result, err)
	}
	result, err = c.Control(ctx, sess, surface.ControlRequest{Action: "model", Value: "opus"})
	if err != nil || result.Output != "opus" || sess.ConfiguredModel != "opus" {
		t.Fatalf("model = %+v, %v; configured %q", result, err, sess.ConfiguredModel)
	}
	state, err := c.Requests(ctx, sess)
	if err != nil || state.PermissionMode != "bypassPermissions" {
		t.Fatalf("state = %+v, %v", state, err)
	}
	data, err := os.ReadFile(filepath.Join(fixture.ClaudeDir, "controls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range claudehosttest.Lines(data) {
		var control struct{ Subtype, Mode, Model string }
		if err := json.Unmarshal(line, &control); err != nil {
			t.Fatal(err)
		}
		got = append(got, control.Subtype+"="+control.Mode+control.Model)
	}
	if strings.Join(got, ",") != "set_permission_mode=bypassPermissions,set_model=opus" {
		t.Fatalf("controls Claude received = %v", got)
	}
}

func TestAPermissionModeChangeNeedsTheHost(t *testing.T) {
	c := NewClaude(nil, t.TempDir())
	sess := &surface.Session{ID: uuid.NewString(), Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: os.Getpid(), LastActive: time.Now()}
	if _, err := c.Control(context.Background(), sess, surface.ControlRequest{Action: "permission-mode", Value: "plan"}); err == nil {
		t.Fatal("a CLI session changed its permission mode")
	}
	if _, err := c.Requests(context.Background(), sess); !errors.Is(err, surface.ErrUnsupported) {
		t.Fatalf("requests of a CLI session = %v, want unsupported", err)
	}
}
