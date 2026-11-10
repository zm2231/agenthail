package daemon

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type paritySetup = func(*testing.T, *Daemon, *registry.Registry)

func failing(operation string, err error) paritySetup {
	return func(t *testing.T, d *Daemon, _ *registry.Registry) {
		t.Helper()
		adapter := d.Surfaces[0].(paritySurface)
		adapter.fail = map[string]error{operation: err}
		d.Surfaces = []surface.Surface{adapter}
	}
}

func all(setups ...paritySetup) paritySetup {
	return func(t *testing.T, d *Daemon, r *registry.Registry) {
		t.Helper()
		for _, setup := range setups {
			setup(t, d, r)
		}
	}
}

var parityLocation = surface.Location{Workspace: "w1", Pane: "p1"}

type parityLauncher struct{ located map[string]surface.Location }

func (parityLauncher) ID() string                               { return "parity" }
func (parityLauncher) Agents() []surface.SurfaceKind            { return []surface.SurfaceKind{surface.KindCodex} }
func (parityLauncher) Available(context.Context) (bool, string) { return true, "" }
func (parityLauncher) SessionOptions() bool                     { return true }
func (parityLauncher) Launch(context.Context, surface.LaunchRequest) (surface.LaunchResult, error) {
	return surface.LaunchResult{}, errors.New("parity launcher does not launch")
}
func (l parityLauncher) Locate(context.Context, []surface.Session) map[string]surface.Location {
	return l.located
}

type parityFocuser struct {
	parityLauncher
	err error
}

func (f parityFocuser) Focus(context.Context, surface.Location) error { return f.err }

func withFocusable(launcher surface.Launcher) paritySetup {
	return func(t *testing.T, d *Daemon, r *registry.Registry) {
		t.Helper()
		location := parityLocation
		if err := r.SaveSessionRuntime("to", &surface.Runtime{Launcher: "parity", Location: &location, Focusable: true}); err != nil {
			t.Fatal(err)
		}
		d.transportResolver = nil
		if launcher != nil {
			d.transportResolver = NewSessionTransportResolver([]surface.Launcher{launcher})
		}
	}
}

func withoutSurfaces(_ *testing.T, d *Daemon, _ *registry.Registry) {
	d.Surfaces = nil
}

func withAliasOn(name, sessionID string) paritySetup {
	return func(t *testing.T, _ *Daemon, r *registry.Registry) {
		t.Helper()
		if err := r.SetAlias(name, sessionID); err != nil {
			t.Fatal(err)
		}
	}
}

func withAliasHeld(name string) paritySetup {
	return func(t *testing.T, _ *Daemon, r *registry.Registry) {
		t.Helper()
		if _, err := r.ReserveAlias(name, time.Date(2099, 1, 1, 12, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
}

func withMoveInFlight(_ *testing.T, d *Daemon, _ *registry.Registry) {
	d.limitResumes.claims.moving = map[string]bool{"to": true}
}

type parityMover struct {
	paritySurface
	prepare error
}

func (m parityMover) PrepareMove(context.Context, *surface.Session, surface.AdoptRequest) (*surface.MoveOut, error) {
	return nil, m.prepare
}
func (parityMover) ArchiveMove(string, string) error                 { return errors.New("unused") }
func (parityMover) RestoreMove(string, string) error                 { return errors.New("unused") }
func (parityMover) WriteMoveArchive(io.Writer, string, string) error { return errors.New("unused") }
func (parityMover) CheckMoveIn(surface.MoveIn) error                 { return errors.New("unused") }
func (parityMover) ReceiveMove(context.Context, surface.MoveIn, io.Reader, func() error) (*surface.Session, error) {
	return nil, errors.New("unused")
}
func (parityMover) DropMoveIn(context.Context, string, string, func() error, func() error) error {
	return errors.New("unused")
}
func (parityMover) MoveToRoot(context.Context, *surface.MoveOut, string, string) (*surface.Session, error) {
	return nil, errors.New("unused")
}
func (parityMover) RecoverRootMove(context.Context, string, string, string) (bool, error) {
	return false, errors.New("unused")
}

func withMoverPreparing(err error) paritySetup {
	return func(t *testing.T, d *Daemon, _ *registry.Registry) {
		t.Helper()
		d.Surfaces = []surface.Surface{parityMover{paritySurface: d.Surfaces[0].(paritySurface), prepare: err}}
	}
}

func withSendFailing(t *testing.T, d *Daemon, _ *registry.Registry) {
	t.Helper()
	d.Surfaces[0].(paritySurface).sendErr = errors.New("the desktop app closed")
}

func withCompactFailing(t *testing.T, d *Daemon, _ *registry.Registry) {
	t.Helper()
	d.Surfaces[0].(paritySurface).compactErr = errors.New("compaction refused")
}

func parityFailureCases() []parityCase {
	session := func(name, action, extra string, setup paritySetup) parityCase {
		return parityCase{name: name, action: action, body: `{"action":"` + action + `","sessionId":"to"` + extra + `}`, setup: setup}
	}
	broken := errors.New("the runtime broke")
	return []parityCase{
		session("alias taken", "alias", `,"alias":"@taken"`, withAliasOn("taken", "from")),
		session("alias held for a starting session", "alias", `,"alias":"held"`, withAliasHeld("held")),
		session("alias of the lead", "alias", `,"alias":"`+registry.LeadAlias+`"`, nil),
		session("session-focus focused", "session-focus", ``, withFocusable(parityFocuser{parityLauncher: parityLauncher{located: map[string]surface.Location{"to": parityLocation}}})),
		session("session-focus without a launcher transport", "session-focus", ``, withFocusable(nil)),
		session("session-focus by a launcher that cannot focus", "session-focus", ``, withFocusable(parityLauncher{})),
		session("session-focus without its surface", "session-focus", ``, all(withFocusable(parityFocuser{}), withoutSurfaces)),
		session("session-focus when listing fails", "session-focus", ``, all(withFocusable(parityFocuser{}), failing("list", broken))),
		session("session-focus at a stale location", "session-focus", ``, withFocusable(parityFocuser{parityLauncher: parityLauncher{located: map[string]surface.Location{"to": {Workspace: "elsewhere"}}}})),
		session("session-focus failing", "session-focus", ``, withFocusable(parityFocuser{parityLauncher: parityLauncher{located: map[string]surface.Location{"to": parityLocation}}, err: broken})),
		session("session-adopt busy", "session-adopt", ``, failing("adopt", &surface.AdoptBusyError{Session: "to", Reason: "a turn is running"})),
		session("session-adopt needs consent", "session-adopt", ``, failing("adopt", &surface.AdoptConsentError{Session: "to", PID: 4242})),
		session("session-adopt refused", "session-adopt", ``, failing("adopt", surface.DeliveryTerminal(errors.New("the session ended")))),
		session("session-adopt outcome unknown", "session-adopt", ``, failing("adopt", surface.DeliveryOutcomeUnknown(errors.New("the runtime stopped answering")))),
		session("session-adopt failing", "session-adopt", ``, failing("adopt", broken)),
		session("limit-move while moving", "limit-move", `,"configRoot":"work"`, all(withLimitStop, withMoveInFlight)),
		session("limit-move of an unmovable surface", "limit-move", `,"configRoot":"work"`, withLimitStop),
		session("limit-move busy", "limit-move", `,"configRoot":"work"`, all(withLimitStop, withMoverPreparing(&surface.AdoptBusyError{Session: "to", Reason: "a turn is running"}))),
		session("limit-move needs consent", "limit-move", `,"configRoot":"work"`, all(withLimitStop, withMoverPreparing(&surface.AdoptConsentError{Session: "to", PID: 4242}))),
		session("limit-move refused", "limit-move", `,"configRoot":"work"`, all(withLimitStop, withMoverPreparing(MoveRefusedError{Reason: "the session is archived"}))),
		session("limit-move failing", "limit-move", `,"configRoot":"work"`, all(withLimitStop, withMoverPreparing(broken))),
		session("model control failed", "model", `,"model":"gpt"`, failing("control", broken)),
		session("interrupt control failed", "interrupt", ``, failing("control", broken)),
		session("permission-mode control failed", "permission-mode", `,"permissionMode":"plan"`, failing("control", broken)),
		session("send failing", "send", `,"message":"hello"`, withSendFailing),
		session("steer refused as invalid", "steer", `,"message":"nudge"`, failing("control", surface.DeliveryTerminal(errors.New("steer needs a running turn"), surface.DeliveryInvalidRequest))),
		session("steer failing", "steer", `,"message":"nudge"`, failing("control", broken)),
		session("goal-pause refused as invalid", "goal-pause", ``, failing("goal", surface.DeliveryTerminal(errors.New("no goal is set"), surface.DeliveryInvalidRequest))),
		session("compact failing", "compact", ``, withCompactFailing),
		session("goal-pause failing", "goal-pause", ``, failing("goal", broken)),
		session("session-fork failing", "session-fork", `,"alias":"forked"`, failing("fork", broken)),
		session("session-fork outcome unknown", "session-fork", `,"alias":"forked"`, failing("fork", surface.DeliveryOutcomeUnknown(errors.New("the runtime stopped answering")))),
		session("session-lifecycle-stop failing", "session-lifecycle-stop", ``, failing("lifecycle", broken)),
		session("native-queue failing", "native-queue", `,"nativeQueue":{"queueAction":"list"}`, failing("native-queue", broken)),
		session("respond choice when requests fail", "respond", `,"requestId":"ask-r1","decision":{"choices":["o0"]}`, failing("requests", broken)),
		session("respond failing", "respond", `,"requestId":"ask-r1","decision":{"choices":["allow"]}`, failing("respond", broken)),
	}
}
