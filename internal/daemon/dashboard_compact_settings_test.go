package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type compactSettingsSurface struct {
	*turnSettingsSurface
	compact []string
}

func (s *compactSettingsSurface) CompactSettings(context.Context, *surface.Session) ([]surface.TurnSetting, error) {
	return []surface.TurnSetting{surface.AutoCompactSetting(true), surface.CompactWindowSetting("", surface.TurnSettingAppliesNewSessions, "default")}, nil
}

func (s *compactSettingsSurface) SetCompactSetting(_ context.Context, _ *surface.Session, key, value string) error {
	if key == surface.CompactSettingWindow {
		if err := surface.ValidCompactWindow(value); err != nil {
			return err
		}
	}
	s.compact = append(s.compact, key+"="+value)
	return nil
}

func TestCompactSettingsReachSessionMetadataAndTheTurnSettingAction(t *testing.T) {
	d, fake, from := turnSettingsDaemon(t)
	compact := &compactSettingsSurface{turnSettingsSurface: fake}
	d = newTestDaemon(t, d.Registry, []surface.Surface{compact})

	response := serveDashboardRequest(dashboardRouter(d), http.MethodGet, "/api/session-metadata?id="+from.ID, "")
	var body struct {
		CompactSettings []surface.TurnSetting `json:"compactSettings"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || len(body.CompactSettings) != 2 || body.CompactSettings[1].Scope != "default" {
		t.Fatalf("status=%d body=%s err=%v", response.Code, response.Body.String(), err)
	}

	ctx := context.Background()
	person := principal{Kind: principalPerson}
	for _, change := range [][2]string{{surface.CompactSettingAuto, surface.CompactAutoOff}, {surface.CompactSettingWindow, "400000"}} {
		if _, err := d.doSessionAction(ctx, person, dashboardActionRequest{SessionID: from.ID, Action: "turn-setting", Setting: change[0], Value: change[1]}); err != nil {
			t.Fatalf("%s: %v", change[0], err)
		}
	}
	if _, err := d.doSessionAction(ctx, person, dashboardActionRequest{SessionID: from.ID, Action: "turn-setting", Setting: surface.CompactSettingWindow, Value: "5"}); err == nil {
		t.Fatal("a window Claude ignores was accepted")
	}
	if got := strings.Join(compact.compact, ","); got != "autoCompact=off,compactWindow=400000" || len(fake.changes) != 0 {
		t.Fatalf("compact=%s turn=%v", got, fake.changes)
	}
}
