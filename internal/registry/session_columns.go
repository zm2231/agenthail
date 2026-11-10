package registry

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func sessionColumnsSQL(status string) string {
	return `s.id,s.surface,s.name,s.cwd,s.pid,` + status + `,s.ended,s.transcript,s.has_local,s.source,s.transport,s.runtime_kind,s.configured_model,s.bridge,s.forked_from,s.last_active_ms,COALESCE(sr.runtime_launcher,''),sr.runtime_location,COALESCE(sr.runtime_focusable,0),COALESCE(cs.waiting_on,X''),COALESCE(cs.background_tasks,X''),COALESCE(cs.usage_limit,X''),COALESCE(cs.turn_stop,X''),COALESCE(cs.settings,X''),` + sessionSubagentSelect
}

type sessionScan struct {
	session                                                         surface.Session
	kind, status, launcher                                          string
	hasLocal, focusable                                             int
	lastActiveMS                                                    int64
	location, waitingOn, background, usageLimit, turnStop, settings []byte
	subagent                                                        subagentColumns
}

func (c *sessionScan) targets() []any {
	s := &c.session
	return append([]any{&s.ID, &c.kind, &s.Name, &s.Cwd, &s.PID, &c.status, &s.Ended, &s.Transcript, &c.hasLocal, &s.Source, &s.Transport, &s.RuntimeKind, &s.ConfiguredModel, &s.Bridge, &s.ForkedFrom, &c.lastActiveMS, &c.launcher, &c.location, &c.focusable, &c.waitingOn, &c.background, &c.usageLimit, &c.turnStop, &c.settings}, c.subagent.targets()...)
}

func (c *sessionScan) result() (surface.Session, error) {
	session := c.session
	session.Surface = surface.SurfaceKind(c.kind)
	session.Status = surface.SessionStatus(c.status)
	session.HasLocal = c.hasLocal != 0
	if c.lastActiveMS > 0 {
		session.LastActive = time.UnixMilli(c.lastActiveMS)
	}
	c.subagent.apply(&session)
	var err error
	if session.Runtime, err = runtimeFromColumns(c.launcher, c.location, c.focusable); err != nil {
		return surface.Session{}, err
	}
	if session.WaitingOn, err = decodeCatalogWaiting(c.waitingOn); err != nil {
		return surface.Session{}, err
	}
	if session.BackgroundTasks, err = decodeCatalogBackground(c.background); err != nil {
		return surface.Session{}, err
	}
	if session.UsageLimit, err = decodeCatalogUsageLimit(c.usageLimit); err != nil {
		return surface.Session{}, err
	}
	if session.TurnStop, err = decodeCatalogTurnStop(c.turnStop); err != nil {
		return surface.Session{}, err
	}
	if session.Settings, err = decodeCatalogSettings(c.settings); err != nil {
		return surface.Session{}, err
	}
	return session, nil
}
