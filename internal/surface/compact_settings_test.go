package surface

import (
	"errors"
	"testing"
)

func TestCompactWindowSettingOffersAutoThePresetsAndACustomWindow(t *testing.T) {
	setting := CompactWindowSetting("350000", TurnSettingAppliesNewSessions, "work")
	var labels []string
	for _, option := range setting.Options {
		labels = append(labels, option.Label)
	}
	if got := labels; len(got) != 7 || got[0] != "Auto" || got[1] != "200k" || got[5] != "1M" || got[6] != "350k" {
		t.Fatalf("options = %v", got)
	}
	if setting.Scope != "work" || setting.Applies != TurnSettingAppliesNewSessions {
		t.Fatalf("setting = %+v", setting)
	}
	if len(CompactWindowSetting("400000", TurnSettingAppliesNow, "").Options) != 6 {
		t.Fatal("a preset window is listed twice")
	}
}

func TestValidCompactWindowTakesAutoOrAWindowClaudeHonours(t *testing.T) {
	for _, value := range []string{"auto", "100000", "1000000"} {
		if err := ValidCompactWindow(value); err != nil {
			t.Fatalf("%s: %v", value, err)
		}
	}
	for _, value := range []string{"", "99999", "1000001", "200k", "unset"} {
		if err := ValidCompactWindow(value); !errors.Is(err, ErrInvalidTurnSetting) {
			t.Fatalf("%q was accepted: %v", value, err)
		}
	}
}
