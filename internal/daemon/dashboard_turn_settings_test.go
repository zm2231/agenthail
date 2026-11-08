package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type turnSettingsSurface struct {
	*daemonSurface
	settings []surface.TurnSetting
	changes  []string
	fail     error
}

func (s *turnSettingsSurface) TurnSettings(context.Context, *surface.Session) ([]surface.TurnSetting, error) {
	return s.settings, nil
}

func (s *turnSettingsSurface) SetTurnSetting(_ context.Context, sess *surface.Session, key, value string) error {
	if s.fail != nil {
		return s.fail
	}
	if key == surface.TurnSettingModel {
		sess.ConfiguredModel = value
		s.changes = append(s.changes, key+"="+value)
		return nil
	}
	if err := surface.ValidTurnSetting(s.settings, key, value); err != nil {
		return err
	}
	s.changes = append(s.changes, key+"="+value)
	return nil
}

func turnSettingsDaemon(t *testing.T) (*Daemon, *turnSettingsSurface, surface.Session) {
	t.Helper()
	_, registry, _, from, _ := daemonFixture(t)
	fake := &turnSettingsSurface{
		daemonSurface: &daemonSurface{kind: from.Surface, sessions: map[string]surface.Session{from.ID: from}, accepted: true},
		settings:      []surface.TurnSetting{{Key: surface.TurnSettingEffort, Value: "low", Options: []surface.TurnSettingOption{{ID: "low", Label: "Low"}, {ID: "high", Label: "High"}}, Applies: surface.TurnSettingAppliesNextTurn}},
	}
	return newTestDaemon(t, registry, []surface.Surface{fake}), fake, from
}

func TestSessionMetadataCarriesTheTurnSettings(t *testing.T) {
	d, fake, from := turnSettingsDaemon(t)
	response := serveDashboardRequest(dashboardRouter(d), http.MethodGet, "/api/session-metadata?id="+from.ID, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		TurnSettings []surface.TurnSetting `json:"turnSettings"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.TurnSettings) != 1 || body.TurnSettings[0].Key != "effort" || body.TurnSettings[0].Applies != "nextTurn" || len(body.TurnSettings[0].Options) != len(fake.settings[0].Options) {
		t.Fatalf("turnSettings=%+v", body.TurnSettings)
	}
}

func TestATurnSettingActionReachesTheAdapterAndNamesWhatItCannotDo(t *testing.T) {
	d, fake, from := turnSettingsDaemon(t)
	ctx := context.Background()
	person := principal{Kind: principalPerson}
	if _, err := d.doSessionAction(ctx, person, dashboardActionRequest{SessionID: from.ID, Action: "turn-setting", Setting: "effort", Value: "high"}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(fake.changes) != "[effort=high]" {
		t.Fatalf("changes=%v", fake.changes)
	}
	status := func(request dashboardActionRequest) (int, string, map[string]any) {
		_, err := d.doSessionAction(ctx, person, request)
		var failed *actionError
		if !errors.As(err, &failed) {
			t.Fatalf("%+v: err=%v", request, err)
		}
		return failed.Status, failed.Code, failed.Extra
	}
	if code, _, _ := status(dashboardActionRequest{SessionID: from.ID, Action: "turn-setting", Setting: "effort", Value: "ultra"}); code != http.StatusBadRequest {
		t.Fatalf("an effort the session does not offer: %d", code)
	}
	if code, _, _ := status(dashboardActionRequest{SessionID: from.ID, Action: "turn-setting", Setting: "mode", Value: "plan"}); code != http.StatusBadRequest {
		t.Fatalf("a setting the session does not have: %d", code)
	}
	fake.fail = &surface.RuntimeVersionError{Runtime: "Codex", Feature: "Changing a thread's model, effort or mode", Minimum: "0.133.0"}
	code, name, extra := status(dashboardActionRequest{SessionID: from.ID, Action: "turn-setting", Setting: "effort", Value: "high"})
	if code != http.StatusConflict || name != "runtime_too_old" || extra["minimum"] != "0.133.0" || extra["runtime"] != "Codex" {
		t.Fatalf("an old runtime: %d %s %v", code, name, extra)
	}
	if fmt.Sprint(fake.changes) != "[effort=high]" {
		t.Fatalf("a failed change was recorded: %v", fake.changes)
	}
}

func TestAClaudeModelTurnSettingIsKeptInTheRegistry(t *testing.T) {
	_, registry, _, _, _ := daemonFixture(t)
	claude := surface.Session{ID: "claude-host", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle}
	if err := registry.RegisterSession(claude); err != nil {
		t.Fatal(err)
	}
	fake := &turnSettingsSurface{daemonSurface: &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{claude.ID: claude}, accepted: true}}
	d := newTestDaemon(t, registry, []surface.Surface{fake})
	if _, err := d.doSessionAction(context.Background(), principal{Kind: principalPerson}, dashboardActionRequest{SessionID: claude.ID, Action: "turn-setting", Setting: "model", Value: "opus"}); err != nil {
		t.Fatal(err)
	}
	kept, err := registry.Session(claude.ID)
	if err != nil || kept.ConfiguredModel != "opus" {
		t.Fatalf("kept=%+v err=%v", kept, err)
	}
}
