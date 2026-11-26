package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

const askDefaultTellWithin = 24 * time.Hour

func askDefault(ask registry.Ask) (int, bool) {
	if ask.Origin != registry.AskOriginAgent || ask.Deadline.IsZero() || len(ask.Questions) != 1 {
		return 0, false
	}
	for i, option := range ask.Questions[0].Options {
		if option.ID == ask.Questions[0].Default {
			return i, true
		}
	}
	return 0, false
}

func askDefaultMessage(ask registry.Ask) string {
	index, ok := askDefault(ask)
	if !ok {
		return ""
	}
	return "[agenthail] No answer by " + ask.Deadline.Local().Format("Jan 2 15:04 MST") + "; the default applies: " + ask.Questions[0].Options[index].Label + "."
}

// The session is told from the asks that took their default lately, not from the ones this sweep expired, so a stop
// between the two still tells it; the delivery key keeps it to one message.
func (d *Daemon) applyAskDefaults(now time.Time) {
	expired, err := d.Registry.ApplyAskDefaults(now)
	if err != nil {
		d.log.Printf("ask defaults: apply: %s", err)
	}
	for _, ask := range expired {
		if ask.RoutedTo != registry.AskToPerson {
			if _, err := d.Registry.CancelPendingByKey(askNoticeKey(ask.ID, ask.RoutedTo)); err != nil {
				d.log.Printf("ask defaults: withdraw the notice of %s: %s", ask.ID, err)
			}
		}
		d.withdrawAsk(ask.ID)
		d.publishAsk(ask.SessionID)
	}
	defaulted, err := d.Registry.DefaultedAsksSince(now.Add(-askDefaultTellWithin))
	if err != nil {
		d.log.Printf("ask defaults: read: %s", err)
		return
	}
	for _, ask := range defaulted {
		if _, ok := askDefault(ask); !ok {
			continue
		}
		if _, err := d.sendNotice(noticeRecipient{sessionID: ask.SessionID, role: "asker"}, askDefaultMessage(ask), "ask-default:"+ask.ID); err != nil {
			d.log.Printf("ask defaults: tell %s about %s: %s", d.resolveDisplay(ask.SessionID), ask.ID, err)
		}
	}
}
