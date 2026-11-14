package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdQueue(args []string) error {
	if a.registry() == nil {
		return fmt.Errorf("queue requires the registry")
	}
	if len(args) > 0 && args[0] == "list" {
		if hasFlag(args, "--help") {
			fmt.Println("usage: agenthail queue list [--json] [--all] [--target <target>] [--mine] [--project <name|id>] [--cwd <path>]")
			fmt.Println("  --target   show messages for one destination")
			fmt.Println("  --mine     show messages sent by or addressed to the caller session")
			fmt.Println("  --project  show messages for targets in one project")
			fmt.Println("  --cwd      show messages for targets in the project that holds this folder")
			fmt.Println("  --all     include terminal queue history")
			return nil
		}
		if len(stripFlags(args)) != 1 {
			return fmt.Errorf("usage: agenthail queue list [--json] [--all] [--target <target>] [--mine] [--project <name|id>] [--cwd <path>]")
		}
		rows, err := a.registry().ListQueue(hasFlag(args, "--all"), a.adapters())
		if err != nil {
			return err
		}
		rows, err = a.filterQueueRows(context.Background(), rows, flagVal(args, "--target"), hasFlag(args, "--mine"), strings.TrimSpace(flagVal(args, "--project")), flagVal(args, "--cwd"))
		if err != nil {
			return err
		}
		if hasFlag(args, "--json") {
			return emit(map[string]any{"messages": rows})
		}
		if len(rows) == 0 {
			fmt.Println("(queue empty)")
			return nil
		}
		for _, row := range rows {
			hostID, _ := a.Registry.SessionHost(row.SessionID)
			fmt.Printf("#%-4d %-26s attempts=%d target=%s %s\n", row.ID, queueStatusLabel(row, hostID != ""), row.Attempts, a.resolveDisplay(row.SessionID), truncate(strings.ReplaceAll(row.Message, "\n", " "), 100))
			if row.Status == "pending" {
				fmt.Printf("      queued %s; %s\n", relTime(queuedTime(row.QueuedAt)), queueHoldLabel(row.HeldBy))
			}
			if row.DeferredSinceMS > 0 && (row.Status == "pending" || row.Status == "inflight") {
				fmt.Printf("      deferred %d times since %s: %s\n", row.Deferrals, relTime(time.UnixMilli(row.DeferredSinceMS)), row.LastError)
			} else if row.LastError != "" {
				fmt.Printf("      last error: %s\n", row.LastError)
			}
		}
		return nil
	}
	if len(args) == 2 && (args[0] == "rm" || args[0] == "cancel" || args[0] == "clear") {
		if err := a.queueManageScope(context.Background(), args[0], args[1]); err != nil {
			return err
		}
	}
	if len(args) > 0 && (args[0] == "rm" || args[0] == "cancel") {
		if len(args) != 2 {
			return fmt.Errorf("usage: agenthail queue rm <id>")
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("invalid queue id %q", args[1])
		}
		if item, err := a.registry().QueueItem(id, a.adapters()); err == nil && item.Status == "dead" {
			return fmt.Errorf("queue item #%d failed; answer its ask instead: agenthail ask list", id)
		}
		if _, err := a.registry().CancelMessage(id); err != nil {
			return err
		}
		fmt.Printf("queue item #%d canceled\n", id)
		return nil
	}
	if len(args) > 0 && args[0] == "clear" {
		if len(args) != 2 {
			return fmt.Errorf("usage: agenthail queue clear <target>")
		}
		sessionID, err := a.queueSessionID(context.Background(), args[1])
		if err != nil {
			return err
		}
		count, err := a.registry().CancelMessagesForSession(sessionID)
		if err != nil {
			return err
		}
		fmt.Printf("canceled %d queued message(s) for %s\n", count, a.resolveDisplay(sessionID))
		return nil
	}
	positional := stripFlags(args)
	if len(positional) < 2 {
		return errors.New(queueUsage)
	}
	ctx := context.Background()
	sess, surf, remote, err := a.resolveDeliveryTarget(ctx, positional[0])
	if err != nil {
		return fmt.Errorf("%w\n%s", err, queueUsage)
	}
	scope := daemon.ScopeRequest{Action: "send", Mode: "queue"}
	if remote != nil {
		scope.Target = remote.ID
	} else if spec, coordinated := a.coordinator().Of(sess.ID); coordinated {
		scope = daemon.ScopeRequest{Action: "lead-send", Mode: "queue", Project: spec.ProjectID}
	} else {
		scope.Target = sess.ID
	}
	if _, err := a.agentScope(ctx, scope); err != nil {
		return err
	}
	source, err := a.sourceSessionID(ctx, "")
	if err != nil {
		return err
	}
	if remote != nil {
		message, err := messageText(strings.Join(positional[1:], " "))
		if err != nil {
			return err
		}
		if message == "" {
			return fmt.Errorf("message is empty")
		}
		return a.queueRemote(ctx, remote, message, source)
	}
	if a.registry() == nil {
		return fmt.Errorf("queue requires the registry")
	}
	if surf.Name() == surface.KindNotion && (sess.ID == "new" || strings.HasPrefix(sess.ID, "new:")) {
		return fmt.Errorf("a new Notion thread cannot be queued before it has a persisted UUID; use 'agenthail send %s ...'", positional[0])
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	message, err := messageText(strings.Join(positional[1:], " "))
	if err != nil {
		return err
	}
	if positional[1] != "-" && len(message) > 8000 {
		fmt.Fprintf(os.Stderr, "warning: message is %d chars; shell may have truncated it. Use '-' to read from stdin for long messages.\n", len(message))
	}
	if message == "" {
		return fmt.Errorf("message is empty")
	}
	warnQueuedWithoutDaemon(os.Stderr)
	if _, _, err := a.registry().QueueDeliveryWithIntent(sess.ID, message, "", surface.SendOptions{BusyDelivery: string(deliverypolicy.Queue), SourceSessionID: source}); err != nil {
		return err
	}
	if waiting, err := a.registry().SessionWaiting(sess.ID); err == nil && waiting != nil {
		fmt.Printf("queued for %s, which is waiting on you; it delivers after you answer\n", a.resolveDisplay(sess.ID))
	} else {
		fmt.Printf("queued for %s (delivered when the target is idle on a daemon scan)\n", a.resolveDisplay(sess.ID))
	}
	return nil
}

func (a *App) filterQueueRows(ctx context.Context, rows []registry.QueueRow, targetSelector string, mine bool, projectSelector, cwd string) ([]registry.QueueRow, error) {
	targetID := ""
	if targetSelector != "" {
		sessionID, err := a.queueSessionID(ctx, targetSelector)
		if err != nil {
			return nil, fmt.Errorf("resolve --target: %w", err)
		}
		targetID = sessionID
	}
	mineID := ""
	if mine {
		var err error
		mineID, err = a.sourceSessionID(ctx, "")
		if err != nil {
			return nil, err
		}
		if mineID == "" {
			return nil, fmt.Errorf("--mine requires a caller session binding; set AGENTHAIL_SESSION_ID, CODEX_THREAD_ID, or CLAUDE_CODE_SESSION_ID")
		}
	}
	if projectSelector != "" && cwd != "" {
		return nil, fmt.Errorf("choose either --cwd or --project")
	}
	if projectSelector != "" || cwd != "" {
		projects, err := daemon.CatalogSessionProjects(a.registry())
		if err != nil {
			return nil, err
		}
		projectID, err := a.scopeProject(ctx, projects, projectSelector, cwd)
		if err != nil {
			return nil, err
		}
		rows = projects.In(rows, projectID)
	}
	filtered := make([]registry.QueueRow, 0, len(rows))
	for _, row := range rows {
		if targetID != "" && row.SessionID != targetID {
			continue
		}
		if mineID != "" && row.SessionID != mineID && row.SourceSessionID != mineID {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered, nil
}
