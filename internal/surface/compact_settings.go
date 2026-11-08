package surface

import (
	"context"
	"fmt"
	"slices"
	"strconv"
)

const (
	CompactSettingAuto          = "autoCompact"
	CompactSettingWindow        = "compactWindow"
	CompactSettingSessionWindow = "sessionCompactWindow"
)

const (
	TurnSettingAppliesNewSessions = "newSessions"
	TurnSettingAppliesNextMessage = "nextMessage"
)

const CompactWindowRootDefault = "default"

func IsCompactSetting(key string) bool {
	return key == CompactSettingAuto || key == CompactSettingWindow || key == CompactSettingSessionWindow
}

func SessionCompactWindowSetting(value string, pending *string) TurnSetting {
	setting := CompactWindowSetting(value, TurnSettingAppliesNextMessage, "")
	setting.Key = CompactSettingSessionWindow
	setting.Options = append([]TurnSettingOption{{ID: CompactWindowRootDefault, Label: "Root default"}}, setting.Options...)
	setting.Pending = pending
	return setting
}

const (
	CompactAutoOn  = "on"
	CompactAutoOff = "off"
	CompactAuto    = "auto"
)

var compactWindowPresets = []int64{200_000, 400_000, 600_000, 800_000, 1_000_000}

type CompactSettingsProvider interface {
	CompactSettings(ctx context.Context, sess *Session) ([]TurnSetting, error)
	SetCompactSetting(ctx context.Context, sess *Session, key, value string) error
}

func AutoCompactSetting(enabled bool) TurnSetting {
	value := CompactAutoOff
	if enabled {
		value = CompactAutoOn
	}
	return TurnSetting{
		Key:     CompactSettingAuto,
		Value:   value,
		Options: []TurnSettingOption{{ID: CompactAutoOn, Label: "On"}, {ID: CompactAutoOff, Label: "Off"}},
		Applies: TurnSettingAppliesNow,
	}
}

func CompactWindowSetting(value, applies, scope string) TurnSetting {
	options := []TurnSettingOption{{ID: CompactAuto, Label: "Auto"}}
	tokens, _ := strconv.ParseInt(value, 10, 64)
	for _, preset := range compactWindowPresets {
		options = append(options, TurnSettingOption{ID: strconv.FormatInt(preset, 10), Label: CompactWindowLabel(preset)})
	}
	if tokens > 0 && !slices.Contains(compactWindowPresets, tokens) {
		options = append(options, TurnSettingOption{ID: value, Label: CompactWindowLabel(tokens)})
	}
	return TurnSetting{Key: CompactSettingWindow, Value: value, Options: options, Applies: applies, Scope: scope}
}

func CompactWindowLabel(tokens int64) string {
	if tokens%1_000_000 == 0 {
		return strconv.FormatInt(tokens/1_000_000, 10) + "M"
	}
	if tokens%1_000 == 0 {
		return strconv.FormatInt(tokens/1_000, 10) + "k"
	}
	return strconv.FormatInt(tokens, 10)
}

func ValidCompactWindow(value string) error {
	if value == CompactAuto {
		return nil
	}
	tokens, err := strconv.ParseInt(value, 10, 64)
	if err != nil || tokens < ClaudeAutoCompactMinTokens || tokens > ClaudeAutoCompactMaxTokens {
		return fmt.Errorf("%w: a compact window is auto or a token count from %d to %d; got %q", ErrInvalidTurnSetting, ClaudeAutoCompactMinTokens, ClaudeAutoCompactMaxTokens, value)
	}
	return nil
}
