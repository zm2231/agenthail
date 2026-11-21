package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/registry"
)

type eventOccurrence struct {
	EventID   string         `json:"eventId"`
	Name      string         `json:"name"`
	Timestamp string         `json:"timestamp"`
	Data      map[string]int `json:"data"`
	Cursor    *string        `json:"cursor"`
}

func (m *remoteMCP) pokeEvents() {
	select {
	case m.eventWake <- struct{}{}:
	default:
	}
}

func (d *Daemon) runEventDeliveries(ctx context.Context) {
	m := d.remoteMCP()
	var follow <-chan struct{}
	if path, err := d.Registry.ChangeSignal(registry.ChangeAgentInbox); err != nil {
		d.log.Printf("follow agent inboxes for event deliveries, checking every %s instead: %s", eventSweep, err)
	} else {
		wake := filewake.Follow(path)
		defer wake.Release()
		follow = wake.C()
	}
	ticker := time.NewTicker(eventSweep)
	defer ticker.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	var mu sync.Mutex
	inFlight := map[string]bool{}
	for {
		if _, err := d.Registry.PruneExpiredEventSubscriptions(time.Now()); err != nil {
			d.logRuntimeError("prune event subscriptions", err)
		}
		due, err := d.Registry.DueEvents(time.Now(), m.eventTrialEvery)
		if err != nil {
			d.logRuntimeError("event deliveries", err)
		}
		mu.Lock()
		for _, event := range due {
			if inFlight[event.ID] || len(inFlight) >= eventDeliveries {
				continue
			}
			inFlight[event.ID] = true
			wg.Add(1)
			go func() {
				defer wg.Done()
				m.deliverEvent(ctx, event)
				mu.Lock()
				delete(inFlight, event.ID)
				mu.Unlock()
				m.pokeEvents()
			}()
		}
		mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.eventWake:
		case <-follow:
		}
	}
}

func retryable(status int) bool {
	return status == 0 || status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status >= 500
}

func (m *remoteMCP) deliverEvent(ctx context.Context, event registry.DueEvent) {
	occurrence := eventOccurrence{EventID: "evt_" + uuid.NewString(), Name: event.Name, Timestamp: time.Now().UTC().Format(time.RFC3339), Data: map[string]int{"unread": event.Unread}}
	body, err := json.Marshal(occurrence)
	if err != nil {
		m.d.logRuntimeError("event deliveries", err)
		return
	}
	schedule := m.eventRetries
	// A suspended subscription gets one trial, not a retry run.
	if !event.SuspendedAt.IsZero() {
		schedule = schedule[:1]
	}
	reason, wait := "", time.Duration(0)
	for attempt, delay := range schedule {
		wait = max(jitter(delay), wait)
		if wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		sub, live, err := m.d.Registry.LiveEventSubscription(event.ID, time.Now())
		if err != nil {
			m.d.logRuntimeError("event deliveries", err)
			return
		}
		if !live || sub.Generation != event.Generation {
			return
		}
		reply := m.postWebhook(ctx, sub.ID, sub.URL, occurrence.EventID, body, sub.Secrets(time.Now()))
		if ctx.Err() != nil {
			return
		}
		switch {
		case reply.reason == "" && reply.status >= 200 && reply.status < 300:
			if err := m.d.Registry.EventDelivered(sub.ID, sub.Generation, event.Newest); err != nil {
				m.d.logRuntimeError("event deliveries", err)
			}
			m.recordEvent(sub.IdentityID, sub.URL, fmt.Sprintf("delivered %s after %d attempt(s)", occurrence.EventID, attempt+1))
			return
		case reply.status == http.StatusGone:
			if err := m.d.Registry.DropEventSubscription(sub.ID, sub.Generation); err != nil {
				m.d.logRuntimeError("event deliveries", err)
			}
			m.recordEvent(sub.IdentityID, sub.URL, "disabled: the endpoint answered 410 Gone")
			return
		}
		reason = reply.reason
		if reply.status != 0 {
			reason = fmt.Sprintf("%s (%d)", reply.reason, reply.status)
		}
		if !retryable(reply.status) {
			break
		}
		wait = 0
		if reply.retryAfter > 0 {
			if attempt+1 < len(schedule) && reply.retryAfter <= eventRetryAfterInRun {
				wait = reply.retryAfter
				continue
			}
			until := time.Now().Add(reply.retryAfter)
			if err := m.d.Registry.DeferEvent(sub.ID, sub.Generation, until); err != nil {
				m.d.logRuntimeError("event deliveries", err)
			}
			m.recordEvent(sub.IdentityID, sub.URL, fmt.Sprintf("deferred %s until %s, as the endpoint asked: %s", occurrence.EventID, until.UTC().Format(time.RFC3339), reason))
			return
		}
	}
	suspended, err := m.d.Registry.EventFailed(event.ID, event.Generation, eventSuspendAfter)
	if err != nil {
		m.d.logRuntimeError("event deliveries", err)
	}
	m.recordEvent(event.IdentityID, event.URL, "failed "+occurrence.EventID+": "+reason)
	if suspended {
		m.recordEvent(event.IdentityID, event.URL, fmt.Sprintf("suspended after %d failed events; tried again every %s", eventSuspendAfter, m.eventTrialEvery))
	}
}

func jitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	return time.Duration(float64(delay) * (0.8 + 0.4*rand.Float64()))
}
