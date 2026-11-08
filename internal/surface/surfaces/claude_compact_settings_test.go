package surfaces

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAHostSessionTurnsAutocompactOffNowAndItsWindowGoesToTheRoot(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	ctx := context.Background()

	settings, err := c.CompactSettings(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	if auto := settingValue(t, settings, surface.CompactSettingAuto); auto.Value != surface.CompactAutoOn || auto.Applies != surface.TurnSettingAppliesNow {
		t.Fatalf("autoCompact = %+v", auto)
	}
	if window := settingValue(t, settings, surface.CompactSettingWindow); window.Value != "" || window.Applies != surface.TurnSettingAppliesNewSessions || window.Scope != "default" {
		t.Fatalf("compactWindow = %+v", window)
	}
	if err := c.SetCompactSetting(ctx, sess, surface.CompactSettingAuto, surface.CompactAutoOff); err != nil {
		t.Fatal(err)
	}
	if err := c.SetCompactSetting(ctx, sess, surface.CompactSettingWindow, "400000"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetCompactSetting(ctx, sess, surface.CompactSettingWindow, "50000"); !errors.Is(err, surface.ErrInvalidTurnSetting) {
		t.Fatalf("a window Claude ignores was accepted: %v", err)
	}
	c.host.listed = time.Time{}
	settings, err = c.CompactSettings(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	if got := settingValue(t, settings, surface.CompactSettingAuto).Value; got != surface.CompactAutoOff {
		t.Fatalf("autoCompact after turning it off = %q", got)
	}
	if got := settingValue(t, settings, surface.CompactSettingWindow).Value; got != "400000" {
		t.Fatalf("compactWindow after the change = %q", got)
	}
	data, err := os.ReadFile(filepath.Join(fixture.Home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(data, "autoCompactWindow").Int(); got != 400000 {
		t.Fatalf("root settings = %s", data)
	}
}

func TestAWindowChangeGoesToTheModelsOwnWindowWhenTheRootHasOne(t *testing.T) {
	file := surface.ClaudeCompactSettings{AutoCompactWindow: &surface.ClaudeCompactWindow{Tokens: 300000}, ModelWindows: map[string]surface.ClaudeCompactWindow{"claude-opus-5-5": {Auto: true}}}
	if key, value := claudeWindowFor(file, "claude-opus-5-5"); key != "claude-opus-5-5" || value != surface.CompactAuto {
		t.Fatalf("model window: %q %q", key, value)
	}
	if key, value := claudeWindowFor(file, "claude-sonnet-5-5"); key != "" || value != "300000" {
		t.Fatalf("root window: %q %q", key, value)
	}
}

func TestOnlyAHostSessionTurnsAutocompactOffHere(t *testing.T) {
	c := NewClaude(nil, t.TempDir())
	sess := &surface.Session{ID: uuid.NewString(), Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: os.Getpid()}
	if err := c.SetCompactSetting(context.Background(), sess, surface.CompactSettingAuto, surface.CompactAutoOff); !errors.Is(err, surface.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	settings, err := c.CompactSettings(context.Background(), sess)
	if err != nil || len(settings) != 1 || settings[0].Key != surface.CompactSettingWindow {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
}
