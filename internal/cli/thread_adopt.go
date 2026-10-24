package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const threadAdoptUsage = `usage: agenthail thread adopt <target> [--wait <duration>] [--stop-terminal] [--json]`

func (a *App) cmdThreadAdopt(args []string) (adoptErr error) {
	jsonOut := hasFlag(args, "--json")
	defer func() {
		if adoptErr != nil && jsonOut {
			_ = emit(map[string]any{"ok": false, "unknown": surface.IsDeliveryOutcomeUnknown(adoptErr), "error": adoptErr.Error()})
		}
	}()
	if err := validateThreadAdoptArgs(args); err != nil {
		return err
	}
	positional := stripFlags(args)
	if len(positional) != 2 {
		return errors.New(threadAdoptUsage)
	}
	request := surface.AdoptRequest{StopTerminal: hasFlag(args, "--stop-terminal")}
	if wait := flagVal(args, "--wait"); wait != "" {
		duration, err := time.ParseDuration(wait)
		if err != nil || duration < 0 {
			return fmt.Errorf("--wait must be a duration such as 5m")
		}
		request.Wait = duration
	}
	timeout, err := commandTimeout(args, a.DefaultTimeout+request.Wait)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	session, adapter, remote, err := a.resolveAnyTarget(ctx, positional[1])
	if err != nil {
		return err
	}
	if remote != nil {
		return fmt.Errorf("adopting a session that runs on another host is not supported; run it on that host")
	}
	adopter, ok := adapter.(surface.SessionAdopter)
	if !ok {
		return fmt.Errorf("%s sessions cannot be opened in Agenthail", session.Surface)
	}
	result, err := adopter.AdoptSession(ctx, session, request)
	if err != nil {
		var consent *surface.AdoptConsentError
		if errors.As(err, &consent) && !jsonOut {
			return fmt.Errorf("%w; pass --stop-terminal to stop it", err)
		}
		return err
	}
	if a.registry() != nil && result.Session != nil {
		if err := a.registry().RegisterSession(*result.Session); err != nil {
			return fmt.Errorf("session %s opened in Agenthail, but its registration failed: %w", session.ID, err)
		}
		_ = a.registry().RecordHistory(registry.HistoryEntry{Kind: "session-operation", SessionID: session.ID, Result: "adopt"})
	}
	if jsonOut {
		return emit(map[string]any{"ok": true, "result": result})
	}
	label := session.ID
	if session.Name != "" {
		label = session.Name + " (" + session.ID + ")"
	}
	if result.Unchanged {
		fmt.Printf("%s already runs in Agenthail\n", label)
		return nil
	}
	fmt.Printf("opened %s in Agenthail\n", label)
	if result.Stopped != "" {
		fmt.Printf("  stopped the %s that held it\n", result.Stopped)
	}
	return nil
}

func validateThreadAdoptArgs(args []string) error {
	values := map[string]bool{"--wait": true, "--timeout": true}
	bools := map[string]bool{"--json": true, "--stop-terminal": true}
	for i := 1; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			continue
		}
		switch {
		case values[args[i]]:
			i++
		case bools[args[i]]:
		default:
			return fmt.Errorf("thread adopt does not accept %s\n%s", args[i], threadAdoptUsage)
		}
	}
	return nil
}
