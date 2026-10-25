package daemon

import (
	"context"
	"fmt"
	"strconv"

	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) ensureDashboardWritable(ctx context.Context, adapter surface.Surface, session *surface.Session) error {
	if err := surface.EnsureWritableSession(ctx, adapter, session); err != nil {
		return err
	}
	if err := d.Registry.RegisterSession(*session); err != nil {
		return err
	}
	return nil
}

func (d *Daemon) control(ctx context.Context, adapter surface.Surface, session *surface.Session, request surface.ControlRequest) (*surface.ControlResult, error) {
	if controller, ok := adapter.(surface.Controller); ok {
		result, err := controller.Control(ctx, session, request)
		d.dispatcher().RecordControl(result, err)
		return result, err
	}
	result := &surface.ControlResult{SessionID: session.ID, Action: request.Action, Evidence: surface.EvidenceDelivered, Source: string(adapter.Name())}
	var err error
	if request.Action == "interrupt" {
		err = adapter.Interrupt(ctx, session)
	} else if setter, ok := adapter.(surface.ModelSetter); ok && request.Value != "" {
		var change surface.ModelChange
		change, err = setter.SetModel(ctx, session, request.Value)
		result.Output, result.Change = change.Text(), &change
	} else {
		result.Output, err = adapter.Model(ctx, session, request.Value)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func pinHolder(ctx context.Context, adapter surface.Surface, session *surface.Session, pid int, startedAt int64) error {
	if session.Surface != surface.KindClaude || pid < 0 {
		return fmt.Errorf("pid names a Claude process that holds the conversation")
	}
	pinned, err := adapter.Resolve(ctx, strconv.Itoa(pid))
	if err != nil || pinned.ID != session.ID || pinned.PID != pid || startedAt != 0 && pinned.StartedAt.UnixMilli() != startedAt {
		return fmt.Errorf("Claude process %d does not hold conversation %s", pid, session.ID)
	}
	session.PID, session.HolderPID, session.HolderStart, session.Transport, session.Bridge = pinned.PID, pinned.HolderPID, pinned.HolderStart, pinned.Transport, pinned.Bridge
	return nil
}
