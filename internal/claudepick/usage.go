package claudepick

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	SessionCeiling = 90
	WeeklyCeiling  = 90
)

type Window struct {
	Percent  float64
	ResetsAt time.Time
	Ceiling  float64
}

type Candidate struct {
	Name      string
	Root      string
	Account   string
	Login     string
	Primary   bool
	Session   *Window
	Weekly    *Window
	Pool      []Member
	Best      string
	FullUntil time.Time
}

func known(c Candidate) bool { return c.Session != nil || c.Weekly != nil || !c.FullUntil.IsZero() }

func ceiling(w *Window, fallback float64) float64 {
	if w != nil && w.Ceiling > 0 {
		return w.Ceiling
	}
	return fallback
}

func under(c Candidate) bool {
	return c.FullUntil.IsZero() && (c.Session == nil || c.Session.Percent < ceiling(c.Session, SessionCeiling)) && (c.Weekly == nil || c.Weekly.Percent < ceiling(c.Weekly, WeeklyCeiling))
}

func load(c Candidate) float64 {
	value := 0.0
	if c.Session != nil {
		value = math.Max(value, c.Session.Percent/ceiling(c.Session, SessionCeiling))
	}
	if c.Weekly != nil {
		value = math.Max(value, c.Weekly.Percent/ceiling(c.Weekly, WeeklyCeiling))
	}
	return value
}

func frees(c Candidate) time.Time {
	if !c.FullUntil.IsZero() {
		return c.FullUntil
	}
	var at time.Time
	for _, window := range []struct {
		reading *Window
		ceiling float64
	}{{c.Session, ceiling(c.Session, SessionCeiling)}, {c.Weekly, ceiling(c.Weekly, WeeklyCeiling)}} {
		if window.reading != nil && window.reading.Percent >= window.ceiling && window.reading.ResetsAt.After(at) {
			at = window.reading.ResetsAt
		}
	}
	if at.IsZero() {
		return time.Unix(math.MaxInt32, 0)
	}
	return at
}

func readings(c Candidate) string {
	parts := []string{}
	if c.Best != "" {
		parts = append(parts, "next: "+c.Best)
	}
	if c.Session != nil {
		parts = append(parts, fmt.Sprintf("session %.0f%%", c.Session.Percent))
	}
	if c.Weekly != nil {
		parts = append(parts, fmt.Sprintf("week %.0f%%", c.Weekly.Percent))
	}
	return strings.Join(parts, ", ")
}
