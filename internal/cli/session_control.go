package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdStream(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail stream <target>")
	}
	timeout, err := commandTimeout(args, 10*time.Minute)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	sess, surf, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		sess = remote.session()
	} else if !surface.CapabilitiesFor(surf, sess).Stream {
		return fmt.Errorf("%s does not support stream", surf.Name())
	}
	if a.daemonIsRunning() {
		page, err := a.readSessionPage(ctx, surf, sess, surface.SessionReadRequest{Limit: 1})
		if err != nil {
			return fmt.Errorf("read active daemon stream cursor: %w", err)
		}
		if page.UnavailableReason != "" {
			return fmt.Errorf("active daemon session source unavailable: %s", page.UnavailableReason)
		}
		var reader sessionStreamReader
		if a.daemonSessionStreamReader != nil {
			reader, err = a.daemonSessionStreamReader()
		} else {
			reader, err = newDaemonSessionStreamClient()
		}
		if err != nil {
			return fmt.Errorf("open active daemon session stream: %w", err)
		}
		if reader == nil {
			return errors.New("open active daemon session stream: reader is unavailable")
		}
		subscription, err := reader.OpenSessionStream(ctx, sess, 0)
		if err != nil {
			return err
		}
		defer subscription.Cancel()
		bodies := map[string]string{}
		versions := map[string]uint64{}
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case event, ok := <-subscription.Events:
				if !ok {
					return errors.New("daemon session stream ended")
				}
				if event.Kind == "source-error" {
					return fmt.Errorf("session source failed: %s", event.Reason)
				}
				if event.Seq <= page.JournalSeq {
					continue
				}
				printSessionStreamEvent(event, bodies, versions)
			}
		}
	}
	return streamDirectOutput(ctx, surf, sess, "", timeout)
}

func (a *App) cmdCompact(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail compact <target>")
	}
	ctx := context.Background()
	sess, surf, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		return a.compactRemote(ctx, remote)
	}
	if !surface.EffectiveCapabilities(surf, sess).Compact {
		return fmt.Errorf("%s does not support compact", surf.Name())
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	receipt, err := (delivery.Dispatcher{Registry: a.registry()}).Compact(ctx, surf, sess)
	if err != nil {
		return err
	}
	if receipt.Evidence == surface.EvidenceQueued {
		fmt.Printf("compact queued for %s as #%d; it will run when the target is idle\n", a.resolveDisplay(sess.ID), receipt.QueueID)
		return nil
	}
	fmt.Printf("compacted %s\n", a.resolveDisplay(sess.ID))
	return nil
}

func (a *App) cmdModel(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 || len(positional) > 2 {
		return fmt.Errorf("usage: agenthail model <target> [name] [--json] [--timeout 30s]")
	}
	ctx, cancel, err := commandContext(args)
	if err != nil {
		return err
	}
	defer cancel()
	sess, surf, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		name := ""
		if len(positional) > 1 {
			name = positional[1]
		}
		return a.modelRemote(ctx, remote, name, args)
	}
	if !surface.EffectiveCapabilities(surf, sess).Model {
		return fmt.Errorf("%s does not support model switching", surf.Name())
	}
	name := ""
	if len(positional) > 1 {
		name = positional[1]
		if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
			return err
		}
	}
	if controller, ok := surf.(surface.Controller); ok && name != "" {
		result, controlErr := controller.Control(ctx, sess, surface.ControlRequest{Action: "model", Value: name})
		if controlErr == nil {
			a.recordHistory(registry.HistoryEntry{Kind: "model", SessionID: sess.ID, Message: name, Result: "changed by " + daemon.CLIActor})
		}
		if controlErr == nil && a.registry() != nil {
			if err := a.registry().RegisterSession(*sess); err != nil {
				return fmt.Errorf("persist Claude model state: %w", err)
			}
		}
		return a.printControlResult(sess, result, controlErr, args)
	}
	if setter, ok := surf.(surface.ModelSetter); ok && name != "" {
		change, err := setter.SetModel(ctx, sess, name)
		if err != nil {
			return err
		}
		a.recordHistory(registry.HistoryEntry{Kind: "model", SessionID: sess.ID, Message: name, Result: change.Text()})
		return printModelChange(sess.ID, change, args)
	}
	current, err := surf.Model(ctx, sess, name)
	if err != nil {
		return err
	}
	change := surface.ModelChange{Model: current}
	if reader, ok := surf.(surface.PendingModelReader); ok && name == "" {
		change.PendingModel = reader.PendingModel(sess.ID)
	}
	return printModelChange(sess.ID, change, args)
}

func printModelChange(sessionID string, change surface.ModelChange, args []string) error {
	if hasFlag(args, "--json") {
		return emit(struct {
			SessionID string `json:"sessionId"`
			surface.ModelChange
		}{sessionID, change})
	}
	if change.Model != "" {
		fmt.Println(change.Text())
	}
	return nil
}

func (a *App) cmdControl(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 2 {
		return fmt.Errorf("usage: agenthail control <target> <context|usage|status|effort|rename> [value] [--json] [--timeout 30s]")
	}
	action, value := positional[1], strings.Join(positional[2:], " ")
	switch action {
	case "context", "usage", "status", "effort", "rename":
	default:
		return fmt.Errorf("unknown control %q; use model, interrupt, steer or compact for those", action)
	}
	ctx, cancel, err := commandContext(args)
	if err != nil {
		return err
	}
	defer cancel()
	sess, surf, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		return a.controlRemote(ctx, remote, action, value, args)
	}
	controller, ok := surf.(surface.Controller)
	if !ok || !surface.EffectiveCapabilities(surf, sess).Commands {
		return fmt.Errorf("%s does not support Claude Remote Control commands", surf.Name())
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	result, controlErr := controller.Control(ctx, sess, surface.ControlRequest{Action: action, Value: value})
	return a.printControlResult(sess, result, controlErr, args)
}

func (a *App) printControlResult(sess *surface.Session, result *surface.ControlResult, controlErr error, args []string) error {
	if result == nil {
		return controlErr
	}
	(delivery.Dispatcher{Registry: a.registry()}).RecordControl(result, controlErr)
	if hasFlag(args, "--json") {
		if err := emit(result); err != nil {
			return err
		}
		return controlErr
	}
	printControl(a.resolveDisplay(sess.ID), result)
	return controlErr
}

func printControl(target string, result *surface.ControlResult) {
	request := ""
	if result.RequestID != "" {
		request = " (request " + result.RequestID + ")"
	}
	fmt.Printf("%s %s for %s%s\n", result.Action, result.Evidence, target, request)
	if result.Detail != "" {
		fmt.Println(result.Detail)
	}
	if result.Output != "" {
		fmt.Println(result.Output)
	}
}

func commandContext(args []string) (context.Context, context.CancelFunc, error) {
	timeout, err := commandTimeout(args, 30*time.Second)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	return ctx, cancel, nil
}

func (a *App) cmdInterrupt(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail interrupt <target> [--json] [--timeout 30s]")
	}
	ctx, cancel, err := commandContext(args)
	if err != nil {
		return err
	}
	defer cancel()
	sess, surf, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		return a.interruptRemote(ctx, remote, args)
	}
	if !surface.EffectiveCapabilities(surf, sess).Interrupt {
		return fmt.Errorf("%s does not support interrupt", surf.Name())
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	if controller, ok := surf.(surface.Controller); ok {
		result, err := controller.Control(ctx, sess, surface.ControlRequest{Action: "interrupt"})
		return a.printControlResult(sess, result, err, args)
	}
	return surf.Interrupt(ctx, sess)
}

func (a *App) cmdSteer(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 2 {
		return fmt.Errorf("usage: agenthail steer <target> \"message\" ('-' reads stdin) [--json] [--timeout 30s]")
	}
	ctx, cancel, err := commandContext(args)
	if err != nil {
		return err
	}
	defer cancel()
	sess, surf, remote, err := a.resolveDeliveryTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		return a.steerRemote(ctx, remote, strings.Join(positional[1:], " "), hasFlag(args, "--json"))
	}
	if !surface.EffectiveCapabilities(surf, sess).Steer {
		return fmt.Errorf("%s does not support steer", surf.Name())
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	sourceID, err := a.sourceSessionID(ctx, "")
	if err != nil {
		return err
	}
	if sourceID != "" {
		ctx = surface.WithSourceSessionID(ctx, sourceID)
	}
	message, err := messageText(strings.Join(positional[1:], " "))
	if err != nil {
		return err
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("message is empty")
	}
	receipt, err := (delivery.Dispatcher{Registry: a.registry()}).Steer(ctx, surf, sess, message, "")
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(receipt)
	}
	printSteerReceipt(*receipt, a.resolveDisplay(sess.ID))
	return nil
}

func printSteerReceipt(receipt delivery.Receipt, target string) {
	switch {
	case receipt.Evidence == surface.EvidenceQueued:
		fmt.Printf("Queued #%d for %s, which is waiting on you; it delivers after you answer.\n", receipt.QueueID, target)
	case receipt.Status == string(registry.DeliveryIntentSubmitted):
		fmt.Printf("Submitted to %s.\n", target)
	case receipt.Evidence == surface.EvidenceTransportAccepted:
		fmt.Printf("Steer sent to %s (request %s).\n", target, receipt.RequestID)
	default:
		fmt.Printf("Sent to %s.\n", target)
	}
}

const queueUsage = `usage: agenthail queue <target> "message"
       agenthail queue list [--json] [--all] [--target <target>] [--mine] [--project <name|id>] [--cwd <path>]
       agenthail queue retry <id>
       agenthail queue rm <id>
       agenthail queue clear <target>`
