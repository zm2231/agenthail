package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type threadCreateOutput struct {
	OK         bool                `json:"ok"`
	Unknown    bool                `json:"unknown,omitempty"`
	Status     string              `json:"status,omitempty"`
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

const threadUsage = `usage: agenthail thread create <codex|claude> "message" [--cwd <path>] [--alias <name>] [--model <name>] [--approval <untrusted|on-request|never>] [--effort <level>] [--mode <plan|default>] [--service-tier <tier>] [--output-schema <file>] [--name <name>] [--worktree <name>] [--agent <name>] [--permission-mode <mode>] [--timeout <duration>] [--json]
       agenthail thread fork <target> [--cwd <path>] [--model <model>] [--before-turn <id>|--last-turn <id>] [--alias <name>] [--json]
       agenthail thread <status|stop|resume|logs> <target> [--json]
       agenthail thread queue <target> <list|add|update|delete|reorder|start> [message] [--id <id>] [--ids <id,id>] [--client-id <stable-id>] [--cursor <cursor>] [--json]

Codex thread creation uses Codex Desktop. Use 'agenthail codex' for a managed terminal session.`

func (a *App) cmdThread(args []string) error {
	if hasFlag(args, "--help") {
		fmt.Println(threadUsage)
		return nil
	}
	if len(args) > 0 && args[0] != "create" {
		return a.cmdThreadOperation(args)
	}
	request, err := parseThreadCreateRequest(args)
	if err != nil {
		return err
	}
	if a.Registry == nil {
		return fmt.Errorf("registry not available")
	}
	adapter := a.surfaceByKind(request.surface)
	if adapter == nil {
		return fmt.Errorf("surface %s is not configured", request.surface)
	}
	starter, ok := adapter.(surface.SessionStarter)
	if !ok {
		return fmt.Errorf("%s cannot start conversations", request.surface)
	}
	if err := a.Registry.EnsureAliasAvailable(request.alias); err != nil {
		return fmt.Errorf("check thread alias: %w", err)
	}
	timeout, err := commandTimeout(args, a.DefaultTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	session, deliveryResult, startErr := starter.StartSession(ctx, request.options)
	if session != nil {
		if err := a.Registry.RegisterSession(*session); err != nil {
			return emitCreateStorageFailure(request, session, fmt.Sprintf("thread %s was created, but local registration failed: %s; do not retry automatically", session.ID, err))
		}
		if request.alias != "" {
			if err := a.Registry.SetAlias(request.alias, session.ID); err != nil {
				return emitCreateStorageFailure(request, session, fmt.Sprintf("thread %s was created, but its name could not be persisted: %s; do not retry automatically", session.ID, err))
			}
		}
	}

	output := threadCreateOutput{OK: startErr == nil, Session: session, Delivery: deliveryResult, Alias: request.alias}
	if startErr != nil {
		if session != nil {
			intent, intentErr := a.Registry.RecordSessionCreationIntent(session.ID, request.message)
			if intentErr != nil {
				output.Status = "storage_failed"
				output.Retryable = false
				output.Error = fmt.Sprintf("session was created, but its delivery intent could not be persisted: %s; do not retry automatically", intentErr)
				return emitThreadFailure(request, output, fmt.Errorf("%s", output.Error))
			}
			output.DeliveryID = intent.ID
			if surface.IsDeliveryOutcomeUnknown(startErr) {
				recordThreadCreateHistory(a.Registry, "submitted", session, request.message, "", startErr.Error())
				return emitSubmittedThreadOutput(request, output)
			}
			_, _ = a.Registry.FailDeliveryIntent(intent.ID, registry.DeliveryIntentFailed, startErr.Error())
			recordThreadCreateHistory(a.Registry, "failed", session, request.message, "", startErr.Error())
			output.OK = false
			output.Status = "failed"
			output.Retryable = false
			output.Error = startErr.Error()
			return emitThreadFailure(request, output, startErr)
		}
		ambiguous := surface.IsDeliveryOutcomeUnknown(startErr)
		output.Error = startErr.Error()
		kind := "failed"
		if ambiguous {
			output.Error = fmt.Sprintf("initial turn outcome is ambiguous, but no session identity was returned; inspect the provider before any explicit retry: %s", startErr)
		}
		recordThreadCreateHistory(a.Registry, kind, session, request.message, "", output.Error)
		output.Status = "failed"
		output.Retryable = false
		if ambiguous {
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
		if err := a.Registry.MarkDeliveryStarted(session.ID, result, ""); err != nil {
			_ = a.Registry.RecordHistory(registry.HistoryEntry{Kind: "runtime-error", SessionID: session.ID, Message: request.message, Result: result, Error: err.Error()})
		}
	}
	recordThreadCreateHistory(a.Registry, "sent", session, request.message, result, "")
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
	return emitThreadFailure(request, threadCreateOutput{OK: false, Status: "storage_failed", Retryable: false, Session: session, Error: message, Warning: message, Alias: request.alias}, fmt.Errorf("%s", message))
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
		case "--message", "--cwd", "--alias", "--model", "--approval", "--timeout", "--effort", "--mode", "--service-tier", "--output-schema", "--name", "--worktree", "--agent", "--permission-mode":
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
	if request.message == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return threadCreateRequest{}, fmt.Errorf("read stdin: %w", err)
		}
		request.message = string(data)
	}
	request.message = strings.TrimSpace(request.message)
	if request.message == "" {
		return threadCreateRequest{}, fmt.Errorf("message is required\n%s", threadUsage)
	}
	resolvedCwd, err := resolveThreadCwd(flagVal(args, "--cwd"))
	if err != nil {
		return threadCreateRequest{}, err
	}
	request.cwd = resolvedCwd
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
	request.options = surface.SessionStartOptions{Message: request.message, Cwd: request.cwd, Model: request.model, ApprovalPolicy: request.approval, TurnOptions: turnOptions, Name: flagVal(args, "--name"), Worktree: flagVal(args, "--worktree"), Agent: flagVal(args, "--agent"), PermissionMode: flagVal(args, "--permission-mode")}
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
