package surface

import (
	"fmt"
	"strings"
	"time"
)

// StaleSendAge is how old a message may be when it is delivered before its
// text says when it was sent, so the receiver weighs it against what it has
// done since.
const StaleSendAge = 10 * time.Minute

// WithSentAge marks a message sent at sentAt that is delivered StaleSendAge
// or more later, after its sender envelope so the envelope still parses.
func WithSentAge(message string, sentAt, now time.Time) string {
	waited := now.Sub(sentAt)
	if sentAt.IsZero() || waited < StaleSendAge || message == "" {
		return message
	}
	marker := "(sent " + agoText(waited) + " ago) "
	if label, rest, ok := strings.Cut(message, "] "); ok && strings.HasPrefix(message, "[from ") && !strings.Contains(label, "\n") {
		return label + "] " + marker + rest
	}
	return marker + message
}

func agoText(waited time.Duration) string {
	switch {
	case waited < 2*time.Hour:
		return fmt.Sprintf("%d min", int(waited.Minutes()))
	case waited < 48*time.Hour:
		return fmt.Sprintf("%d h", int(waited.Hours()))
	}
	return fmt.Sprintf("%d days", int(waited.Hours()/24))
}
