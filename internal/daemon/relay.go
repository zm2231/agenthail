package daemon

import (
	"fmt"
	"regexp"

	"github.com/zm2231/agenthail/internal/surface"
)

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
		payload := fmt.Sprintf("[relay id=%d source=%s turn=%s] %s", route.ID, d.resolveDisplay(from.ID), completionID, text)
		key := fmt.Sprintf("relay:%d:%s", route.ID, completionID)
		queueID, err := d.Registry.QueueMessageWithKey(route.ToSession, payload, key)
		if err != nil {
			d.log.Printf("queue relay %d: %s", route.ID, err)
			continue
		}
		reserved, err := d.Registry.RecordRelayDelivery(route.ID, completionID)
		if err != nil {
			d.log.Printf("record relay %d after queue item %d: %s", route.ID, queueID, err)
			continue
		}
		if reserved {
			d.log.Printf("relay %d %s -> %s (queued #%d)", route.ID, d.resolveDisplay(from.ID), d.resolveDisplay(route.ToSession), queueID)
		}
	}
}

func matchPattern(pattern, text string) bool {
	if pattern == "" || pattern == ".*" {
		return true
	}
	match, err := regexp.Compile(pattern)
	return err == nil && match.MatchString(text)
}
