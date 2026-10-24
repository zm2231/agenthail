package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdSend(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 2 {
		return fmt.Errorf(`usage: agenthail send <target> "message" [--from <name>] [--stream] [--reply] [--json]`)
	}
	target := positional[0]
	message, err := messageText(strings.Join(positional[1:], " "))
	if err != nil {
		return err
	}
	if positional[1] != "-" && len(message) > 8000 {
		fmt.Fprintf(os.Stderr, "warning: message is %d chars; shell may have truncated it. Use '-' to read from stdin for long messages:\n  echo '...' | agenthail send %s -\n  agenthail send %s - < file.txt\n", len(message), target, target)
	}
	if message == "" {
		return fmt.Errorf("message is empty")
	}
	wantStream := hasFlag(args, "--stream")
	wantReply := hasFlag(args, "--reply")
	jsonOut := hasFlag(args, "--json")
	if wantStream && wantReply {
		return fmt.Errorf("--stream and --reply cannot be combined; choose live deltas or the completed reply")
	}
	if wantStream && jsonOut {
		return fmt.Errorf("--stream and --json cannot be combined; JSON event streaming is not implemented")
	}
	timeout, err := commandTimeout(args, a.DefaultTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	fromLabel := flagVal(args, "--from")
	if fromLabel != "" {
		message = fmt.Sprintf("[from %s] %s", fromLabel, message)
	}

	_, peerLead, err := a.leadTarget(target)
	if err != nil {
		return err
	}
	if peerLead != nil && !hasFlag(args, "--stream") && !hasFlag(args, "--reply") {
		return a.sendToPeerCoordinator(ctx, *peerLead, message, args)
	}
	if target, err = a.expandLead(target); err != nil {
		return err
	}
	coord := a.coordinator()
	_, byName := coord.Named(target)
	if spec, named := coord.Named(target); named && !coord.Started(spec) {
		return a.startCoordinator(ctx, coord, spec, message, args)
	}
	sess, surf, remote, err := a.resolveDeliveryTarget(ctx, target)
	if err != nil {
		return err
	}
	if remote != nil {
		return a.sendRemote(ctx, remote, message, args, timeout)
	}
	spec, coordinated := coord.Of(sess.ID)
	if current, replaced := coord.ReplacedBy(sess.ID); replaced {
		if sess, surf, _, err = a.resolveAnyTarget(ctx, current); err != nil {
			return err
		}
	}
	if !surf.Capabilities().Send {
		return fmt.Errorf("%s does not support send", surf.Name())
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	if surface.RouteFor(surf, sess, "") == surface.RoutePeer && (wantStream || wantReply) {
		return fmt.Errorf("Claude peer message IDs cannot be correlated with transcript turns; use 'last' or native SendMessage replies")
	}

	if wantStream && !surf.Capabilities().Stream {
		return fmt.Errorf("%s does not support stream", surf.Name())
	}
	baseline := ""
	var daemonStream sessionstream.Subscription
	useDaemonStream := a.daemonIsRunning() && (wantStream || wantReply)
	if useDaemonStream {
		daemonStream, err = a.prepareDaemonSessionStream(ctx, surf, sess)
		if err != nil {
			return err
		}
		if daemonStream.Cancel != nil {
			defer daemonStream.Cancel()
		}
	} else if wantReply {
		observation, observeErr := surf.Observe(ctx, sess)
		if observeErr != nil {
			return fmt.Errorf("establish reply cursor before send: %w", observeErr)
		}
		if observation != nil {
			baseline = observation.CompletedTurnID
		}
	}
	turnOptions, err := parseTurnOptions(args)
	if err != nil {
		return err
	}
	options := surface.SendOptions{Model: flagVal(args, "--model"), TurnOptions: turnOptions}
	options.SourceSessionID, err = a.sourceSessionID(ctx, fromLabel)
	if err != nil {
		return err
	}
	addressed := *sess
	dispatcher := delivery.Dispatcher{Registry: a.registry()}
	var receipt *delivery.Receipt
	syntheticNotion := surf.Name() == surface.KindNotion && (sess.ID == "new" || strings.HasPrefix(sess.ID, "new:"))
	syntheticNotionName := ""
	var notionName registry.AliasReservation
	if syntheticNotion {
		syntheticNotionName = sess.Name
		if a.registry() != nil {
			if notionName, err = a.registry().ReserveAlias(syntheticNotionName, time.Now().Add(timeout+threadNameMargin)); err != nil {
				return fmt.Errorf("reserve thread alias: %w", err)
			}
			defer func() { _ = a.registry().ReleaseAliasReservation(notionName) }()
		}
	}
	if coordinated {
		deliver := coord.Deliver
		if hasFlag(args, "--no-queue") {
			deliver = coord.DeliverWithoutQueue
		}
		var outcome *coordinator.Outcome
		if outcome, err = deliver(ctx, spec, message, options); err == nil {
			if outcome.Started {
				if !byName {
					return printCoordinatorStart(outcome, outcome.Session.ID, jsonOut)
				}
				return printCoordinatorStart(outcome, target, jsonOut)
			}
			receipt = outcome.Receipt
			if outcome.Session != nil && outcome.Session.ID != addressed.ID {
				if wantStream || wantReply {
					fmt.Fprintf(os.Stderr, "the coordinator moved to %s while this message was sent, so this send cannot follow its turns; read the reply with 'agenthail last %s'\n", outcome.Session.ID, outcome.Session.ID)
					wantStream, wantReply = false, false
				}
				sess = outcome.Session
			}
		}
	} else if hasFlag(args, "--no-queue") || syntheticNotion {
		receipt, err = dispatcher.DeliverWithoutQueue(ctx, surf, sess, message, "", options)
	} else {
		receipt, err = dispatcher.DeliverWithOptions(ctx, surf, sess, message, "", options)
	}
	if err != nil {
		if syntheticNotion && errors.Is(err, delivery.ErrTargetBusy) {
			return fmt.Errorf("a new Notion thread must be created immediately and cannot be queued; retry 'agenthail send %s ...'", target)
		}
		return err
	}

	if receipt.Evidence == surface.EvidenceQueued {
		if _, ok := daemon.IsRunning(); !ok {
			fmt.Fprintf(os.Stderr, "warning: daemon is not running; queued message will not be delivered until you start it (agenthail daemon start)\n")
		}
		if jsonOut {
			return emit(receipt)
		} else {
			if receipt.WaitingOn != nil {
				fmt.Printf("queued #%d for %s, which is waiting on you; it delivers after you answer\n", receipt.QueueID, a.resolveDisplay(sess.ID))
			} else {
				steerTarget := target
				if coordinated && !byName {
					steerTarget = sess.ID
				}
				fmt.Printf("target is active; queued #%d for %s and will deliver when the current turn finishes (use 'agenthail steer %s \"message\"' to affect the current turn)\n", receipt.QueueID, a.resolveDisplay(sess.ID), steerTarget)
			}
		}
		return nil
	}
	if receipt.Status == string(registry.DeliveryIntentSubmitted) {
		if jsonOut {
			return emit(receipt)
		}
		if receipt.DeliveryID != 0 {
			fmt.Printf("delivery submitted (id %d); %s\n", receipt.DeliveryID, receipt.Detail)
		} else {
			fmt.Printf("delivery submitted; %s\n", receipt.Detail)
		}
		return nil
	}
	if syntheticNotion && receipt.TurnID != "" {
		sess.ID = receipt.TurnID
		sess.Name = syntheticNotionName
		receipt.SessionID = sess.ID
		if a.registry() != nil {
			if err := a.registry().RegisterSession(*sess); err != nil {
				return fmt.Errorf("Notion thread %s was created but could not be registered: %w", sess.ID, err)
			}
			if err := a.registry().BindReservedAlias(notionName, sess.ID); err != nil {
				return fmt.Errorf("Notion thread %s was created but alias %q could not be registered: %w", sess.ID, syntheticNotionName, err)
			}
		}
	}

	if (wantStream || wantReply) && sess.Surface == surface.KindClaude && (sess.ID != addressed.ID || surface.RouteFor(surf, sess, options.Author).TransportOnly()) {
		fmt.Fprintf(os.Stderr, "the closed session was resumed as %s, whose turns this send cannot follow; read the reply with 'agenthail last %s'\n", sess.ID, target)
		wantStream, wantReply = false, false
	}
	if wantStream {
		if useDaemonStream {
			return consumeDaemonStreamOutput(ctx, daemonStream, receipt.TurnID, timeout)
		}
		return streamDirectOutput(ctx, surf, sess, receipt.TurnID, timeout)
	}

	if wantReply {
		var reply *surface.ReplyResult
		if useDaemonStream {
			reply, err = consumeSessionReply(ctx, daemonStream, receipt.TurnID)
		} else {
			reply, err = waitForReply(ctx, surf, sess, baseline, receipt.TurnID, timeout)
		}
		if err != nil {
			return err
		}
		receipt.Evidence = surface.EvidenceReplyObserved
		if a.registry() != nil {
			text := ""
			if reply != nil {
				text = reply.Text
			}
			_ = a.registry().RecordHistory(registry.HistoryEntry{Kind: "reply", SessionID: sess.ID, CompletionID: receipt.TurnID, Result: text})
		}
		if jsonOut {
			return emit(map[string]any{"delivery": receipt, "reply": reply})
		}
		if reply != nil && reply.Text != "" {
			fmt.Println(reply.Text)
		}
	}

	if jsonOut {
		return emit(receipt)
	} else if receipt.Evidence == surface.EvidenceTransportAccepted {
		fmt.Printf("accepted by Claude socket (message %s); receiver policy and model completion are pending\n", receipt.TurnID)
	} else {
		fmt.Printf("sent (turn %s)\n", receipt.TurnID)
	}
	if receipt.Notice != "" && !jsonOut {
		fmt.Println(receipt.Notice)
	}
	return nil
}

func waitForReply(ctx context.Context, adapter surface.Surface, session *surface.Session, baseline, turnID string, timeout time.Duration) (*surface.ReplyResult, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	seenActiveTurn := false
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for reply after %s: %w", timeout, ctx.Err())
		case <-deadline.C:
			return nil, fmt.Errorf("timed out waiting for reply after %s", timeout)
		case <-ticker.C:
			observation, err := adapter.Observe(ctx, session)
			if err != nil {
				return nil, err
			}
			if observation != nil && observation.ActiveTurnID != "" && (turnID == "" || observation.ActiveTurnID == turnID) {
				seenActiveTurn = true
			}
			if observation != nil && adapter.Name() == surface.KindCodex && turnID != "" && observation.TerminalTurnID == turnID && observation.CompletedTurnID != turnID {
				return nil, fmt.Errorf("turn %s ended without an assistant reply", turnID)
			}
			if seenActiveTurn && observation != nil && observation.Status == surface.StatusIdle && observation.ActiveTurnID == "" && observation.CompletedTurnID == baseline {
				return nil, fmt.Errorf("turn %s ended without a completed assistant reply", turnID)
			}
			if observation == nil || observation.Reply == nil || !observation.Reply.Done {
				continue
			}
			if observation.CompletedTurnID == baseline {
				continue
			}
			if observation.Reply.Error != "" {
				return nil, fmt.Errorf("turn %s did not complete successfully: %s", observation.CompletedTurnID, observation.Reply.Error)
			}
			if turnID != "" && observation.CompletedTurnID != turnID && adapter.Name() == surface.KindCodex {
				continue
			}
			return observation.Reply, nil
		}
	}
}

func commandTimeout(args []string, fallback time.Duration) (time.Duration, error) {
	value := flagVal(args, "--timeout")
	if value == "" {
		return fallback, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return 0, fmt.Errorf("--timeout must be a positive duration such as 30s or 2m")
	}
	return timeout, nil
}

func sessionReadBefore(args []string) (int64, error) {
	value := flagVal(args, "--before")
	if value == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor <= 0 {
		return 0, fmt.Errorf("--before must be a positive activity cursor")
	}
	return cursor, nil
}
