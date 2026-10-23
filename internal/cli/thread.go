package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type threadCreateOutput struct {
	OK         bool                `json:"ok"`
	Unknown    bool                `json:"unknown,omitempty"`
	Status     string              `json:"status,omitempty"`
	Code       string              `json:"code,omitempty"`
	Accepted   bool                `json:"accepted,omitempty"`
	Retryable  bool                `json:"retryable"`
	Detail     string              `json:"detail,omitempty"`
	Warning    string              `json:"warning,omitempty"`
	DeliveryID int64               `json:"deliveryId,omitempty"`
	Session    *surface.Session    `json:"session,omitempty"`
	Delivery   *surface.SendResult `json:"delivery,omitempty"`
	Alias      string              `json:"alias,omitempty"`
	Error      string              `json:"error,omitempty"`
}

type threadCreateRequest struct {
	options  surface.SessionStartOptions
	surface  surface.SurfaceKind
	message  string
	cwd      string
	alias    string
	model    string
	approval string
	jsonOut  bool
}

const threadNameMargin = 30 * time.Second

const threadUsage = `usage: agenthail thread create <codex|claude> "message" [--cwd <path>] [--alias <name>] [--model <name>] [--approval <untrusted|on-request|never>] [--effort <level>] [--mode <plan|default>] [--service-tier <tier>] [--output-schema <file>] [--name <name>] [--worktree <name>] [--agent <name>] [--permission-mode <mode>] [--root <name|dir>] [--host <host>] [--from <session>] [--timeout <duration>] [--json]
       agenthail thread fork <target> [--model <model>] [--last-turn <id>] [--alias <name>] [--from <session>] [--json]
       agenthail thread <status|stop|resume|logs> <target> [--json]
       agenthail thread <archive|unarchive> <target> [--json]
       agenthail thread adopt <target> [--wait <duration>] [--stop-terminal] [--json]
       agenthail thread queue <target> <list|add|update|delete|reorder|start> [message] [--id <id>] [--ids <id,id>] [--client-id <stable-id>] [--cursor <cursor>] [--json]

Codex threads start on Codex Desktop when its bridge is reachable and on Codex's app-server daemon otherwise.
--host starts the thread on a paired host; --cwd is then a path on that host.`

func (a *App) cmdThread(args []string) error {
	if hasFlag(args, "--help") {
		fmt.Println(threadUsage)
		return nil
	}
	if len(args) > 0 && (args[0] == "archive" || args[0] == "unarchive") {
		return a.cmdThreadArchive(args)
	}
	if len(args) > 0 && args[0] == "adopt" {
		return a.cmdThreadAdopt(args)
	}
	if len(args) > 0 && args[0] != "create" {
		return a.cmdThreadOperation(args)
	}
	request, err := parseThreadCreateRequest(args)
	if err != nil {
		return err
	}
	if a.registry() == nil {
		return fmt.Errorf("registry not available")
	}
	creator, err := a.sourceSessionID(context.Background(), flagVal(args, "--from"))
	if err != nil {
		return err
	}
	if hostText := strings.TrimSpace(flagVal(args, "--host")); hostText != "" {
		local, err := a.isLocalHost(hostText)
		if err != nil {
			return err
		}
		if !local {
			return a.createRemoteThread(hostText, request, creator, args)
		}
		if request.cwd, err = resolveThreadCwd(request.cwd); err != nil {
			return err
		}
		request.options.Cwd = request.cwd
	}
	adapter := a.adapters().ForSurface(request.surface)
	if adapter == nil {
		return fmt.Errorf("surface %s is not configured", request.surface)
	}
	starter, ok := adapter.(surface.SessionStarter)
	if !ok {
		return fmt.Errorf("%s cannot start conversations", request.surface)
	}
	timeout, err := commandTimeout(args, a.DefaultTimeout)
	if err != nil {
		return err
	}
	reservation, err := a.registry().ReserveAlias(request.alias, time.Now().Add(timeout+threadNameMargin))
	if err != nil {
		return fmt.Errorf("reserve thread alias: %w", err)
	}
	defer func() { _ = a.registry().ReleaseAliasReservation(reservation) }()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	messageUUID := uuid.NewString()
	ctx = surface.WithMessageUUID(ctx, messageUUID)

	session, deliveryResult, startErr := starter.StartSession(ctx, request.options)
	if session != nil {
		if err := a.registry().RegisterSession(*session); err != nil {
			return emitCreateStorageFailure(request, session, fmt.Sprintf("thread %s was created, but local registration failed: %s; do not retry automatically", session.ID, err))
		}
		if err := a.registry().BindReservedAlias(reservation, session.ID); err != nil {
			return emitCreateStorageFailure(request, session, fmt.Sprintf("thread %s was created, but its name could not be persisted: %s; do not retry automatically", session.ID, err))
		}
		if err := a.registry().RecordSessionOrigin(registry.OriginOf(session.ID, creator)); err != nil {
			return emitCreateStorageFailure(request, session, fmt.Sprintf("thread %s was created, but who started it could not be recorded: %s; do not retry automatically", session.ID, err))
		}
	}

	output := threadCreateOutput{OK: startErr == nil, Session: session, Delivery: deliveryResult, Alias: request.alias}
	if startErr != nil {
		if session != nil {
			intent, intentErr := a.registry().RecordSessionCreationIntent(session.ID, surface.SendOptions{SourceSessionID: creator}.Sender(), messageUUID, request.message)
			if intentErr != nil {
				output.Status = "storage_failed"
				output.Code = "create_storage_failed"
				output.Retryable = false
				output.Error = fmt.Sprintf("session was created, but its delivery intent could not be persisted: %s; do not retry automatically", intentErr)
				return emitThreadFailure(request, output, fmt.Errorf("%s", output.Error))
			}
			output.DeliveryID = intent.ID
			if surface.IsDeliveryOutcomeUnknown(startErr) {
				recordThreadCreateHistory(a.registry(), "submitted", session, request.message, "", startErr.Error())
				return emitSubmittedThreadOutput(request, output)
			}
			_, _ = a.registry().FailDeliveryIntentWithNotice(intent.ID, startErr.Error())
			recordThreadCreateHistory(a.registry(), "failed", session, request.message, "", startErr.Error())
			output.OK = false
			output.Status = "failed"
			output.Code = "initial_turn_failed"
			output.Retryable = false
			output.Error = startErr.Error()
			return emitThreadFailure(request, output, startErr)
		}
		output.Error = startErr.Error()
		output.Status, output.Code = "failed", "create_failed"
		if surface.IsDeliveryOutcomeUnknown(startErr) {
			output.Unknown = true
			output.Status, output.Code = "unknown", "create_unknown"
			output.Error = fmt.Sprintf("initial turn outcome is ambiguous, but no session identity was returned; inspect the provider before any explicit retry: %s", startErr)
		}
		recordThreadCreateHistory(a.registry(), output.Status, session, request.message, "", output.Error)
		output.Retryable = false
		if output.Unknown {
			return emitThreadFailure(request, output, fmt.Errorf("%s: %w", output.Error, startErr))
		}
		return emitThreadFailure(request, output, startErr)
	}
	if session == nil {
		return fmt.Errorf("thread creation returned no session")
	}
	result := ""
	if deliveryResult != nil {
		result = deliveryResult.UUID
	}
	if result != "" {
		if err := a.registry().MarkDeliveryStarted(session.ID, result, ""); err != nil {
			_ = a.registry().RecordHistory(registry.HistoryEntry{Kind: "runtime-error", SessionID: session.ID, Message: request.message, Result: result, Error: err.Error()})
		}
	}
	recordThreadCreateHistory(a.registry(), "sent", session, request.message, result, "")
	if request.jsonOut {
		return json.NewEncoder(os.Stdout).Encode(output)
	}
	target := string(session.Surface) + "/" + session.ID
	if request.alias != "" {
		target = "@" + request.alias
	}
	if result == "" {
		fmt.Printf("created %s in %s\n", target, session.Cwd)
	} else {
		fmt.Printf("created %s in %s and started turn %s\n", target, session.Cwd, result)
	}
	return nil
}

func emitSubmittedThreadOutput(request threadCreateRequest, output threadCreateOutput) error {
	output.OK = true
	output.Status = "submitted"
	output.Accepted = true
	output.Retryable = false
	output.Delivery = nil
	output.Detail = submittedThreadDetail(output.Session, request.alias)
	if request.jsonOut {
		return json.NewEncoder(os.Stdout).Encode(output)
	}
	fmt.Println(output.Detail)
	return nil
}

func submittedThreadDetail(session *surface.Session, alias string) string {
	target := alias
	if target != "" {
		target = "@" + target
	} else {
		target = string(session.Surface) + ":" + session.ID
	}
	return "Submitted to " + target + "."
}

func emitCreateStorageFailure(request threadCreateRequest, session *surface.Session, message string) error {
	return emitThreadFailure(request, threadCreateOutput{OK: false, Status: "storage_failed", Code: "create_storage_failed", Retryable: false, Session: session, Error: message, Warning: message, Alias: request.alias}, fmt.Errorf("%s", message))
}

func emitThreadFailure(request threadCreateRequest, output threadCreateOutput, err error) error {
	if request.jsonOut {
		if encodeErr := json.NewEncoder(os.Stdout).Encode(output); encodeErr != nil {
			return fmt.Errorf("write JSON output: %w", encodeErr)
		}
	}
	return err
}

func parseThreadCreateRequest(args []string) (threadCreateRequest, error) {
	if len(args) < 2 || args[0] != "create" {
		return threadCreateRequest{}, fmt.Errorf("%s", threadUsage)
	}
	request := threadCreateRequest{surface: surface.SurfaceKind(strings.ToLower(args[1])), jsonOut: hasFlag(args, "--json")}
	if request.surface != surface.KindCodex && request.surface != surface.KindClaude {
		return threadCreateRequest{}, fmt.Errorf("thread creation requires Codex or Claude")
	}
	var positional []string
	positionalOnly := false
	for i := 2; i < len(args); i++ {
		arg := args[i]
		if positionalOnly {
			positional = append(positional, arg)
			continue
		}
		if arg == "--" {
			positionalOnly = true
			continue
		}
		switch arg {
		case "--message", "--cwd", "--alias", "--model", "--approval", "--timeout", "--effort", "--mode", "--service-tier", "--output-schema", "--name", "--worktree", "--agent", "--permission-mode", "--root", "--host", "--from":
			i++
		case "--json":
		default:
			positional = append(positional, arg)
		}
	}
	messageFlag := flagVal(args, "--message")
	if messageFlag != "" && len(positional) > 0 {
		return threadCreateRequest{}, fmt.Errorf("provide the message either positionally or with --message, not both")
	}
	request.message = messageFlag
	if request.message == "" {
		request.message = strings.Join(positional, " ")
	}
	message, err := messageText(request.message)
	if err != nil {
		return threadCreateRequest{}, err
	}
	request.message = message
	request.message = strings.TrimSpace(request.message)
	if request.message == "" {
		return threadCreateRequest{}, fmt.Errorf("message is required\n%s", threadUsage)
	}
	request.cwd = strings.TrimSpace(flagVal(args, "--cwd"))
	if flagVal(args, "--host") == "" {
		resolvedCwd, err := resolveThreadCwd(request.cwd)
		if err != nil {
			return threadCreateRequest{}, err
		}
		request.cwd = resolvedCwd
	}
	request.alias = strings.TrimPrefix(strings.TrimSpace(flagVal(args, "--alias")), "@")
	request.model = strings.TrimSpace(flagVal(args, "--model"))
	request.approval = strings.TrimSpace(flagVal(args, "--approval"))
	if request.approval != "" && request.approval != "untrusted" && request.approval != "on-request" && request.approval != "never" {
		return threadCreateRequest{}, fmt.Errorf("approval must be untrusted, on-request, or never")
	}
	turnOptions, err := parseTurnOptions(args)
	if err != nil {
		return request, err
	}
	request.options = surface.SessionStartOptions{Message: request.message, Cwd: request.cwd, Model: request.model, ApprovalPolicy: request.approval, TurnOptions: turnOptions, Name: flagVal(args, "--name"), Worktree: flagVal(args, "--worktree"), Agent: flagVal(args, "--agent"), PermissionMode: flagVal(args, "--permission-mode"), ConfigRoot: flagVal(args, "--root")}
	return request, nil
}

func resolveThreadCwd(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current directory: %w", err)
		}
		value = cwd
	} else if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		if value == "~" {
			value = home
		} else {
			value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
		}
	}
	resolved, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory is not a directory")
	}
	return filepath.Clean(resolved), nil
}

func recordThreadCreateHistory(reg *registry.Registry, kind string, session *surface.Session, message, result, errorText string) {
	if reg == nil {
		return
	}
	sessionID := ""
	if session != nil {
		sessionID = session.ID
	}
	_ = reg.RecordHistory(registry.HistoryEntry{Kind: kind, SessionID: sessionID, Message: message, Result: result, Error: errorText})
}
