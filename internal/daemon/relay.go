package daemon

import (
	"fmt"
	"regexp"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const maxRelayText = 24000

func (d *Daemon) fireRelays(from *surface.Session, completionID, text string) {
	routes, err := d.Registry.ListRoutes()
	if err != nil {
		d.log.Printf("list relays: %s", err)
		return
	}
	for _, route := range routes {
		if route.FromSession != from.ID || !matchPattern(route.Pattern, text) {
			continue
		}
		payloadText := text
		if len(payloadText) > maxRelayText {
			payloadText = payloadText[:maxRelayText] + "\n[relay text truncated by agenthail]"
		}
		payload := fmt.Sprintf("[relay id=%d source=%s turn=%s] %s", route.ID, d.resolveDisplay(from.ID), completionID, payloadText)
		key := fmt.Sprintf("relay:%d:%s", route.ID, completionID)
		reserved, err := d.Registry.RecordRelayDelivery(route.ID, completionID)
		if err != nil {
			d.log.Printf("reserve relay %d: %s", route.ID, err)
			continue
		}
		if !reserved {
			continue
		}
		queueID, err := d.Registry.QueueMessageWithKey(route.ToSession, payload, key)
		if err != nil {
			_ = d.Registry.ForgetRelayDelivery(route.ID, completionID)
			d.log.Printf("queue relay %d: %s", route.ID, err)
			continue
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "relay", SessionID: route.ToSession, SourceSessionID: from.ID, RouteID: route.ID, QueueID: queueID, CompletionID: completionID, Message: text, Result: "queued"})
		d.log.Printf("relay %d %s -> %s (queued #%d)", route.ID, d.resolveDisplay(from.ID), d.resolveDisplay(route.ToSession), queueID)
	}
}

func matchPattern(pattern, text string) bool {
	if pattern == "" || pattern == ".*" {
		return true
	}
	match, err := regexp.Compile(pattern)
	return err == nil && match.MatchString(text)
}
