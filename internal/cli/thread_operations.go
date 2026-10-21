package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdThreadOperation(args []string) (operationErr error) {
	defer func() {
		if operationErr != nil && hasFlag(args, "--json") {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": false, "unknown": surface.IsDeliveryOutcomeUnknown(operationErr), "error": operationErr.Error()})
		}
	}()
	if err := validateThreadOperationArgs(args); err != nil {
		return err
	}
	positional := stripFlags(args)
	if len(positional) < 2 {
		return fmt.Errorf("%s", threadUsage)
	}
	timeout, err := commandTimeout(args, a.DefaultTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	operation, target := positional[0], positional[1]
	var session *surface.Session
	var adapter surface.Surface
	if operation == "stop" || operation == "resume" || operation == "logs" || operation == "status" {
		if a.registry() != nil {
			selector := strings.TrimPrefix(target, "claude:")
			if id, e := a.registry().ResolveTarget(selector); e == nil {
				session, _ = a.registry().Session(id)
			}
		}
		if session != nil {
			adapter, err = a.adapters().For(session)
			if err != nil {
				return err
			}
		}
	}
	if session == nil {
		var remote *remoteTarget
		session, adapter, remote, err = a.resolveAnyTarget(ctx, target)
		if err != nil {
			return err
		}
		if remote != nil {
			return a.threadOperationRemote(ctx, remote, operation, positional, args)
		}
	}
	var output any
	switch operation {
	case "fork":
		forker, ok := adapter.(surface.SessionForker)
		if !ok {
			return surface.ErrUnsupported
		}
		var name registry.AliasReservation
		if a.registry() != nil {
			if name, err = a.registry().ReserveAlias(strings.TrimPrefix(flagVal(args, "--alias"), "@"), time.Now().Add(timeout+threadNameMargin)); err != nil {
				return fmt.Errorf("reserve fork alias: %w", err)
			}
			defer func() { _ = a.registry().ReleaseAliasReservation(name) }()
		}
		fork, err := forker.ForkSession(ctx, session, surface.ForkOptions{Model: flagVal(args, "--model"), LastTurnID: flagVal(args, "--last-turn")})
		if err != nil {
			return err
		}
		if fork == nil {
			return fmt.Errorf("fork returned no session")
		}
		if a.registry() != nil {
			if err := a.registry().RegisterSession(*fork); err != nil {
				return err
			}
			if err := a.registry().BindReservedAlias(name, fork.ID); err != nil {
				return err
			}
		}
		output = map[string]any{"session": fork}
	case "queue":
		if len(positional) < 3 {
			return fmt.Errorf("native queue action is required")
		}
		queue, ok := adapter.(surface.NativeQueuer)
		if !ok {
			return surface.ErrUnsupported
		}
		request := surface.NativeQueueRequest{Action: positional[2], Message: strings.Join(positional[3:], " "), ID: flagVal(args, "--id"), ClientID: flagVal(args, "--client-id"), Cursor: flagVal(args, "--cursor")}
		if ids := flagVal(args, "--ids"); ids != "" {
			request.IDs = strings.Split(ids, ",")
		}
		output, err = queue.NativeQueue(ctx, session, request)
	case "status", "stop", "resume", "logs":
		lifecycle, ok := adapter.(surface.SessionLifecycle)
		if !ok {
			return surface.ErrUnsupported
		}
		addressed := *session
		output, err = lifecycle.SessionAction(ctx, session, operation)
		if err == nil && operation == "resume" {
			err = (delivery.Dispatcher{Registry: a.registry()}).RegisterReached(&addressed, session)
		}
	default:
		return fmt.Errorf("%s", threadUsage)
	}
	if err != nil {
		return err
	}
	if a.registry() != nil && operation != "status" && operation != "logs" && !(operation == "queue" && len(positional) > 2 && positional[2] == "list") {
		_ = a.registry().RecordHistory(registry.HistoryEntry{Kind: "session-operation", SessionID: session.ID, Result: strings.Join(positional[:min(3, len(positional))], " ")})
	}
	return json.NewEncoder(os.Stdout).Encode(output)
}

func validateThreadOperationArgs(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", threadUsage)
	}
	allowed := map[string]bool{"--timeout": true, "--json": true}
	switch args[0] {
	case "fork":
		for _, flag := range []string{"--model", "--last-turn", "--alias"} {
			allowed[flag] = true
		}
	case "queue":
		for _, flag := range []string{"--id", "--ids", "--client-id", "--cursor"} {
			allowed[flag] = true
		}
	case "status", "stop", "resume", "logs":
	default:
		return fmt.Errorf("%s", threadUsage)
	}
	for i := 1; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if !strings.HasPrefix(args[i], "--") {
			continue
		}
		if !allowed[args[i]] {
			return fmt.Errorf("%s does not accept %s", args[0], args[i])
		}
		if args[i] != "--json" {
			i++
		}
	}
	return nil
}
