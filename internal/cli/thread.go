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
	OK       bool                `json:"ok"`
	Unknown  bool                `json:"unknown,omitempty"`
	Session  *surface.Session    `json:"session,omitempty"`
	Delivery *surface.SendResult `json:"delivery,omitempty"`
	Alias    string              `json:"alias,omitempty"`
	Error    string              `json:"error,omitempty"`
}

type threadCreateRequest struct {
	surface  surface.SurfaceKind
	message  string
	cwd      string
	alias    string
	model    string
	approval string
	jsonOut  bool
}

const threadUsage = `usage: agenthail thread create codex "message" [--cwd <path>] [--alias <name>] [--model <name>] [--approval <untrusted|on-request|never>] [--timeout <duration>] [--json]`

func (a *App) cmdThread(args []string) error {
	if hasFlag(args, "--help") {
		fmt.Println(threadUsage)
		return nil
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
	timeout, err := commandTimeout(args, a.DefaultTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	session, deliveryResult, startErr := starter.StartSession(ctx, surface.SessionStartOptions{
		Message:        request.message,
		Cwd:            request.cwd,
		Model:          request.model,
		ApprovalPolicy: request.approval,
	})
	if session != nil {
		if err := a.Registry.RegisterSession(*session); err != nil {
			return fmt.Errorf("register created thread %s: %w", session.ID, err)
		}
		if request.alias != "" {
			if err := a.Registry.SetAlias(request.alias, session.ID); err != nil {
				return fmt.Errorf("name created thread %s: %w", session.ID, err)
			}
		}
	}

	output := threadCreateOutput{OK: startErr == nil, Session: session, Delivery: deliveryResult, Alias: request.alias}
	if startErr != nil {
		output.Unknown = surface.IsDeliveryOutcomeUnknown(startErr)
		output.Error = startErr.Error()
		kind := "failed"
		if output.Unknown {
			kind = "unknown"
		}
		recordThreadCreateHistory(a.Registry, kind, session, request.message, "", output.Error)
		if request.jsonOut && session != nil {
			if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
				return fmt.Errorf("write JSON output: %w", err)
			}
		}
		if output.Unknown && session != nil {
			return fmt.Errorf("thread %s was created but its first turn could not be confirmed: %w", session.ID, startErr)
		}
		return startErr
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
	target := "codex/" + session.ID
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

func parseThreadCreateRequest(args []string) (threadCreateRequest, error) {
	if len(args) < 2 || args[0] != "create" {
		return threadCreateRequest{}, fmt.Errorf("%s", threadUsage)
	}
	request := threadCreateRequest{surface: surface.SurfaceKind(strings.ToLower(args[1])), jsonOut: hasFlag(args, "--json")}
	if request.surface != surface.KindCodex {
		return threadCreateRequest{}, fmt.Errorf("only Codex supports non-interactive thread creation")
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
		case "--message", "--cwd", "--alias", "--model", "--approval", "--timeout":
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
