package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAHostSessionTurnSettingsChangeModelEffortAndModeNow(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	ctx := context.Background()

	settings, err := c.TurnSettings(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range settings {
		if setting.Applies != surface.TurnSettingAppliesNow {
			t.Fatalf("%s applies %q on a hosted session", setting.Key, setting.Applies)
		}
	}
	mode := settingValue(t, settings, surface.TurnSettingMode)
	var confirmed []string
	for _, option := range mode.Options {
		if option.Confirm {
			confirmed = append(confirmed, option.ID)
		}
	}
	if strings.Join(confirmed, ",") != "dontAsk,bypassPermissions" {
		t.Fatalf("modes that need a confirmation: %v", confirmed)
	}
	if err := c.SetTurnSetting(ctx, sess, surface.TurnSettingEffort, "turbo"); err == nil {
		t.Fatal("an effort Claude does not offer was accepted")
	}
	for _, change := range [][2]string{{surface.TurnSettingEffort, "xhigh"}, {surface.TurnSettingMode, "plan"}, {surface.TurnSettingModel, "opus"}} {
		if err := c.SetTurnSetting(ctx, sess, change[0], change[1]); err != nil {
			t.Fatalf("set %s: %v", change[0], err)
		}
	}
	settings, err = c.TurnSettings(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	if got := [3]string{settingValue(t, settings, "model").Value, settingValue(t, settings, "effort").Value, settingValue(t, settings, "mode").Value}; got != [3]string{"opus", "xhigh", "plan"} {
		t.Fatalf("settings read back as %v", got)
	}
	data, err := os.ReadFile(filepath.Join(fixture.ClaudeDir, "controls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range claudehosttest.Lines(data) {
		var control struct {
			Subtype, Mode, Model string
			Settings             struct{ EffortLevel string }
		}
		if err := json.Unmarshal(line, &control); err != nil {
			t.Fatal(err)
		}
		got = append(got, control.Subtype+"="+control.Mode+control.Model+control.Settings.EffortLevel)
	}
	if strings.Join(got, ",") != "apply_flag_settings=xhigh,set_permission_mode=plan,set_model=opus" {
		t.Fatalf("controls Claude received = %v", got)
	}
}

func TestAClaudeSessionWithoutTheHostOrRemoteControlHasNoTurnSettings(t *testing.T) {
	c := inDaemon(NewClaude(nil, t.TempDir()))
	c.DisableRemoteControl()
	sess := &surface.Session{ID: uuid.NewString(), Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: os.Getpid()}
	settings, err := c.TurnSettings(context.Background(), sess)
	if err != nil || len(settings) != 0 {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
	if err := c.SetTurnSetting(context.Background(), sess, surface.TurnSettingEffort, "high"); err == nil {
		t.Fatal("an effort change reached a session nothing can change")
	}
}
