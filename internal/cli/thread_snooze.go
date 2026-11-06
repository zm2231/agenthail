package cli

import (
	"fmt"
	"time"
)

const threadSnoozeUsage = "usage: agenthail thread snooze <target> (--until <time|duration> | --until-finished) [--json]\n       agenthail thread wake <target> [--json]"

func (a *App) cmdThreadSnooze(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 2 {
		return fmt.Errorf("%s", threadSnoozeUsage)
	}
	payload := map[string]any{"action": "session-" + positional[0]}
	done := "woke " + positional[1]
	until, finished := flagVal(args, "--until"), hasFlag(args, "--until-finished")
	if positional[0] == "wake" && (until != "" || finished) {
		return fmt.Errorf("%s", threadSnoozeUsage)
	}
	if positional[0] == "snooze" {
		switch {
		case finished && until == "":
			payload["wake"] = "finished"
			done = "snoozed " + positional[1] + " until its turn ends"
		case !finished && until != "":
			at, err := parseSnoozeUntil(until, time.Now())
			if err != nil {
				return err
			}
			payload["until"] = at
			done = "snoozed " + positional[1] + " until " + at.Local().Format("Mon 15:04")
		default:
			return fmt.Errorf("%s", threadSnoozeUsage)
		}
	}
	answer, err := a.threadSessionAction(positional[1], payload)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(answer)
	}
	fmt.Println(done)
	return nil
}

func parseSnoozeUntil(value string, now time.Time) (time.Time, error) {
	if span, err := time.ParseDuration(value); err == nil {
		if span <= 0 {
			return time.Time{}, fmt.Errorf("--until %s is not in the future", value)
		}
		return now.Add(span).UTC(), nil
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("--until takes a duration like 2h or an RFC 3339 time, not %q", value)
	}
	return at.UTC(), nil
}
