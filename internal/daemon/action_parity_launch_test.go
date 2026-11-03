package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type parityLaunch struct {
	parityLauncher
	unavailable string
	result      surface.LaunchResult
	err         error
}

func (l parityLaunch) Available(context.Context) (bool, string) {
	return l.unavailable == "", l.unavailable
}

func (l parityLaunch) Launch(context.Context, surface.LaunchRequest) (surface.LaunchResult, error) {
	return l.result, l.err
}

func withLauncher(launcher surface.Launcher) paritySetup {
	return func(t *testing.T, d *Daemon, r *registry.Registry) {
		t.Helper()
		withFolder("project")(t, d, r)
		d.transportResolver = NewSessionTransportResolver([]surface.Launcher{launcher})
	}
}

func withStartTurnFailing(err error) paritySetup {
	return func(t *testing.T, d *Daemon, r *registry.Registry) {
		t.Helper()
		withFolder("project")(t, d, r)
		d.Surfaces[0].(paritySurface).startErr = err
	}
}

type parityEnsurer struct {
	paritySurface
	err error
}

func (e parityEnsurer) EnsureRuntime(context.Context) error { return e.err }

func withRuntimeEnsurer(err error) paritySetup {
	return func(t *testing.T, d *Daemon, _ *registry.Registry) {
		t.Helper()
		d.Surfaces = []surface.Surface{parityEnsurer{paritySurface: d.Surfaces[0].(paritySurface), err: err}}
	}
}

func parityLaunchCases() []parityCase {
	project := parityFolder("project")
	create := func(name, fields string, setup paritySetup) parityCase {
		return parityCase{name: name, action: "session-create", body: `{"action":"session-create","surface":"codex","cwd":"` + project + `"` + fields + `}`, setup: setup}
	}
	launch := func(name, fields string, launcher surface.Launcher) parityCase {
		return create(name, `,"launcher":"parity"`+fields, withLauncher(launcher))
	}
	location := &surface.Location{Workspace: "w2", Pane: "p2"}
	launched := &surface.Session{ID: "launched", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "launched", Cwd: project, Status: surface.StatusBusy}
	broken := errors.New("the runtime broke")
	return []parityCase{
		create("session-create starts a conversation", `,"message":"go"`, withFolder("project")),
		create("session-create named", `,"message":"go","alias":"@fresh"`, withFolder("project")),
		create("session-create under a held name", `,"message":"go","alias":"held"`, all(withFolder("project"), withAliasHeld("held"))),
		create("session-create from a session", `,"message":"go","sourceSessionId":"from","approvalPolicy":"on-request"`, withFolder("project")),
		create("session-create without a message", ``, withFolder("project")),
		create("session-create in a missing folder", `,"message":"go","cwd":"`+project+`/missing"`, nil),
		create("session-create on an unconfigured surface", `,"message":"go","surface":"claude"`, withFolder("project")),
		create("session-create whose start fails", `,"message":"go"`, all(withFolder("project"), failing("start", broken))),
		create("session-create whose start outcome is unknown", `,"message":"go"`, all(withFolder("project"), failing("start", surface.DeliveryOutcomeUnknown(broken)))),
		create("session-create whose first turn fails", `,"message":"go"`, withStartTurnFailing(broken)),
		create("session-create whose first turn outcome is unknown", `,"message":"go"`, withStartTurnFailing(surface.DeliveryOutcomeUnknown(broken))),
		launch("session-create by a launcher that returns the session", `,"message":"go","alias":"@via"`, parityLaunch{parityLauncher: parityLauncher{}, result: surface.LaunchResult{Session: launched, Location: location, Sent: &surface.SendResult{UUID: "launched-turn", Accepted: true}}}),
		launch("session-create by a launcher that returns a location", `,"message":"go","alias":"@via"`, parityLaunch{result: surface.LaunchResult{Location: location}}),
		launch("session-create by a launcher that returns only an id", `,"message":"go","alias":"@via"`, parityLaunch{result: surface.LaunchResult{SessionID: "unseen", Location: location}}),
		launch("session-create by a launcher that accepts without confirming", `,"message":"go"`, parityLaunch{result: surface.LaunchResult{Location: location}, err: surface.LaunchAcceptedError{Launcher: "parity", Err: broken}}),
		launch("session-create by a launcher refusing an option", `,"message":"go"`, parityLaunch{err: surface.ErrLaunchOptionUnsupported}),
		launch("session-create by a launcher that fails", `,"message":"go"`, parityLaunch{err: broken}),
		launch("session-create by an unavailable launcher", `,"message":"go"`, parityLaunch{unavailable: "the terminal app is closed"}),
		launch("session-create by a launcher without a message", ``, parityLaunch{}),
		launch("session-create by a launcher with a bad name", `,"message":"go","alias":"has space"`, parityLaunch{}),
		create("session-create by an unknown launcher", `,"message":"go","launcher":"nope"`, withLauncher(parityLaunch{})),
		{name: "notion-create under a held name", action: "notion-create", body: `{"action":"notion-create","message":"go","alias":"held"}`, setup: withAliasHeld("held")},
		{name: "runtime-ensure repairs the runtime", action: "runtime-ensure", body: `{"action":"runtime-ensure"}`, setup: withRuntimeEnsurer(nil)},
		{name: "runtime-ensure failing", action: "runtime-ensure", body: `{"action":"runtime-ensure"}`, setup: withRuntimeEnsurer(broken)},
	}
}
