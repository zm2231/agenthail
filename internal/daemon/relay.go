package daemon

import (
	"fmt"
	"regexp"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	maxRelayText = 24000
	maxRelayHops = 8
)

func (d *Daemon) fireRelays(from *surface.Session, completionID string, hops int, text string) {
	if hops >= maxRelayHops {
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "relay-dropped", SourceSessionID: from.ID, CompletionID: completionID, Message: text, Error: "relay hop limit reached"})
		d.log.Printf("drop relay from %s: hop limit %d reached", d.resolveDisplay(from.ID), maxRelayHops)
		return
	}
	routes, err := d.Registry.ListRoutes()
	if err != nil {
		d.log.Printf("list relays: %s", err)
		return
	}
	for _, route := range routes {
		if route.FromSession != from.ID || !matchPattern(route.Pattern, text) {
			continue
		}
		target, targetErr := d.Registry.Session(route.ToSession)
		if targetErr == nil && surface.IsReadOnlySession(target) {
			reason := surface.ReadOnlySessionReason(target)
			_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "relay-dropped", SessionID: route.ToSession, SourceSessionID: from.ID, RouteID: route.ID, CompletionID: completionID, Message: text, Error: reason})
			d.log.Printf("drop relay %d to %s: %s", route.ID, d.resolveDisplay(route.ToSession), reason)
			continue
		}
		payloadText := text
		if len(payloadText) > maxRelayText {
			payloadText = payloadText[:maxRelayText] + "\n[relay text truncated by agenthail]"
		}
		payload := fmt.Sprintf("[agenthail relay hops=%d id=%d source=%s turn=%s] %s", hops+1, route.ID, d.resolveDisplay(from.ID), completionID, payloadText)
		key := fmt.Sprintf("relay:%d:%s", route.ID, completionID)
		queueID, err := d.Registry.QueueRelayMessage(route.ToSession, payload, key, hops+1)
		if err != nil {
			d.log.Printf("queue relay %d: %s", route.ID, err)
			continue
		}
		reserved, err := d.Registry.RecordRelayDelivery(route.ID, completionID)
		if err != nil {
			d.log.Printf("record relay %d after queue item %d: %s", route.ID, queueID, err)
			continue
		}
		if !reserved {
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
