package cli

import (
	"testing"
	"time"
)

func TestThreadSnoozeAndWakeSendTheirActions(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.response = `{"ok":true}`
	for _, args := range [][]string{
		{"thread", "snooze", "vps::@builder", "--until", "2h"},
		{"thread", "snooze", "vps::@builder", "--until-finished"},
		{"thread", "wake", "vps::@builder"},
	} {
		if _, err := captureStdout(t, func() error { return app.Run(args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	actions, _ := api.recorded()
	if len(actions) != 3 {
		t.Fatalf("actions=%v", actions)
	}
	if actions[0]["action"] != "session-snooze" || actions[0]["sessionId"] != vpsBuilder.ID || actions[0]["until"] == nil || actions[0]["wake"] != nil {
		t.Fatalf("until snooze=%v", actions[0])
	}
	if actions[1]["action"] != "session-snooze" || actions[1]["wake"] != "finished" || actions[1]["until"] != nil {
		t.Fatalf("finished snooze=%v", actions[1])
	}
	if actions[2]["action"] != "session-wake" || actions[2]["sessionId"] != vpsBuilder.ID {
		t.Fatalf("wake=%v", actions[2])
	}
}

func TestSnoozeUntilTakesADurationOrATime(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	if at, err := parseSnoozeUntil("90m", now); err != nil || !at.Equal(now.Add(90*time.Minute)) {
		t.Fatalf("90m: %v %v", at, err)
	}
	if at, err := parseSnoozeUntil("2026-11-07T09:00:00-05:00", now); err != nil || !at.Equal(time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC)) {
		t.Fatalf("RFC 3339: %v %v", at, err)
	}
	for _, bad := range []string{"-1h", "0s", "tomorrow"} {
		if _, err := parseSnoozeUntil(bad, now); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
