package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/approval"
)

const maxApprovalHookBytes = 128 << 10

func (a *App) cmdApproval(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agenthail approval <install|uninstall|hook|question-hook|list|allow|deny|answer> ...")
	}
	if args[0] == "install" || args[0] == "uninstall" {
		if len(args) != 1 {
			return fmt.Errorf("usage: agenthail approval %s", args[0])
		}
		if args[0] == "install" {
			return installApprovalHook()
		}
		return uninstallApprovalHook()
	}
	store, err := approval.Open(approval.DefaultPath())
	if err != nil {
		if args[0] == "hook" {
			return writeApprovalHookFailure(fmt.Errorf("approval broker unavailable: %w", err))
		}
		if args[0] == "question-hook" {
			return writeQuestionHookFailure(fmt.Errorf("approval broker unavailable: %w", err))
		}
		return err
	}
	defer store.Close()
	switch args[0] {
	case "hook":
		if len(args) != 1 {
			return fmt.Errorf("usage: agenthail approval hook < request.json")
		}
		return runApprovalHook(store)
	case "question-hook":
		if len(args) != 1 {
			return fmt.Errorf("usage: agenthail approval question-hook < request.json")
		}
		return runQuestionHook(store)
	case "list":
		return listApprovals(store, args[1:])
	case "allow", "approve":
		return decideApproval(store, approval.DecisionAllow, args[1:])
	case "deny", "reject":
		return decideApproval(store, approval.DecisionDeny, args[1:])
	case "answer":
		return answerApproval(store, args[1:])
	default:
		return fmt.Errorf("unknown approval command %q (use install, uninstall, hook, question-hook, list, allow, deny, or answer)", args[0])
	}
}

func approvalSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

func approvalHookCommand() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve agenthail executable: %w", err)
	}
	return shellQuote(executable) + " approval hook", nil
}

func approvalQuestionHookCommand() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve agenthail executable: %w", err)
	}
	return shellQuote(executable) + " approval question-hook", nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func isAgenthailHook(value, suffix string) bool {
	value = strings.TrimSpace(value)
	return strings.HasSuffix(value, suffix) && strings.Contains(value, "agenthail")
}

func installApprovalHook() error {
	command, err := approvalHookCommand()
	if err != nil {
		return err
	}
	questionCommand, err := approvalQuestionHookCommand()
	if err != nil {
		return err
	}
	path, settings, err := readClaudeSettings()
	if err != nil {
		return err
	}
	changed, err := installClaudeHook(settings, "PermissionRequest", "", command, " approval hook")
	if err != nil {
		return err
	}
	questionChanged, err := installClaudeHook(settings, "PreToolUse", "AskUserQuestion", questionCommand, " approval question-hook")
	if err != nil {
		return err
	}
	if !changed && !questionChanged {
		return nil
	}
	return writeClaudeSettings(path, settings)
}

func uninstallApprovalHook() error {
	command, err := approvalHookCommand()
	if err != nil {
		return err
	}
	questionCommand, err := approvalQuestionHookCommand()
	if err != nil {
		return err
	}
	path, settings, err := readClaudeSettings()
	if err != nil {
		return err
	}
	changed, err := uninstallClaudeHook(settings, "PermissionRequest", command)
	if err != nil {
		return err
	}
	questionChanged, err := uninstallClaudeHook(settings, "PreToolUse", questionCommand)
	if err != nil {
		return err
	}
	if !changed && !questionChanged {
		return nil
	}
	return writeClaudeSettings(path, settings)
}

func readClaudeSettings() (string, map[string]any, error) {
	path := approvalSettingsPath()
	settings := map[string]any{}
	if data, readErr := os.ReadFile(path); readErr == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return "", nil, fmt.Errorf("parse Claude settings: %w", err)
		}
	} else if !os.IsNotExist(readErr) {
		return "", nil, fmt.Errorf("read Claude settings: %w", readErr)
	}
	return path, settings, nil
}

func hookEntries(settings map[string]any, event string) (map[string]any, []any, error) {
	hooks, present := settings["hooks"].(map[string]any)
	if _, exists := settings["hooks"]; exists && !present {
		return nil, nil, fmt.Errorf("Claude settings hooks must be an object")
	}
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	entries, present := hooks[event].([]any)
	if _, exists := hooks[event]; exists && !present {
		return nil, nil, fmt.Errorf("Claude settings %s hooks must be an array", event)
	}
	return hooks, entries, nil
}

func installClaudeHook(settings map[string]any, event, matcher, command, staleSuffix string) (bool, error) {
	hooks, entries, err := hookEntries(settings, event)
	if err != nil {
		return false, err
	}
	kept := entries[:0]
	present := false
	changed := false
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		if matcher != "" {
			entryMatcher, _ := entry["matcher"].(string)
			if entryMatcher != matcher {
				kept = append(kept, raw)
				continue
			}
		}
		inner, ok := entry["hooks"].([]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		filtered := inner[:0]
		for _, hookRaw := range inner {
			hook, _ := hookRaw.(map[string]any)
			value, _ := hook["command"].(string)
			if value == command {
				present = true
				filtered = append(filtered, hookRaw)
			} else if isAgenthailHook(value, staleSuffix) {
				changed = true
			} else {
				filtered = append(filtered, hookRaw)
			}
		}
		if len(filtered) == 0 {
			changed = true
			continue
		}
		entry["hooks"] = filtered
		kept = append(kept, raw)
	}
	if !present {
		entry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}
		if matcher != "" {
			entry["matcher"] = matcher
		}
		kept = append(kept, entry)
		changed = true
	}
	hooks[event] = kept
	return changed, nil
}

func uninstallClaudeHook(settings map[string]any, event, command string) (bool, error) {
	hooks, entries, err := hookEntries(settings, event)
	if err != nil {
		return false, err
	}
	kept := entries[:0]
	changed := false
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		inner, ok := entry["hooks"].([]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		filtered := inner[:0]
		for _, hookRaw := range inner {
			hook, _ := hookRaw.(map[string]any)
			if hook["command"] == command {
				changed = true
			} else {
				filtered = append(filtered, hookRaw)
			}
		}
		if len(filtered) > 0 {
			entry["hooks"] = filtered
			kept = append(kept, raw)
		} else if len(filtered) == len(inner) {
			kept = append(kept, raw)
		} else {
			changed = true
		}
	}
	if changed {
		hooks[event] = kept
	}
	return changed, nil
}

func writeClaudeSettings(path string, settings map[string]any) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create Claude settings directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings.json.*")
	if err != nil {
		return fmt.Errorf("create Claude settings temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, bytes.NewReader(append(data, '\n'))); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace Claude settings: %w", err)
	}
	return nil
}

func runApprovalHook(store *approval.Store) error {
	return runHook(store, writeApprovalHookResponse, writeApprovalHookFailure)
}

func runQuestionHook(store *approval.Store) error {
	return runHook(store, writeQuestionHookResponse, writeQuestionHookFailure)
}

func runHook(store *approval.Store, writeResponse func(approval.Response) error, writeFailure func(error) error) error {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxApprovalHookBytes+1))
	if err != nil {
		return writeFailure(fmt.Errorf("read approval request: %w", err))
	}
	if len(data) > maxApprovalHookBytes {
		return writeFailure(fmt.Errorf("approval request exceeds %d bytes", maxApprovalHookBytes))
	}
	request, err := approval.Normalize(data, time.Now())
	if err != nil {
		return writeFailure(err)
	}
	response, _, err := store.Observe(request, time.Now())
	if err != nil {
		return writeFailure(fmt.Errorf("approval broker unavailable: %w", err))
	}
	if response.Decision == approval.DecisionPending {
		deadline := time.Now().Add(approval.DefaultWait)
		if request.ExpiresAt.Before(deadline) {
			deadline = request.ExpiresAt
		}
		for time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
			response, _, err = store.Observe(request, time.Now())
			if err != nil {
				return writeFailure(fmt.Errorf("approval broker unavailable: %w", err))
			}
			if response.Decision != approval.DecisionPending {
				return writeResponse(response)
			}
		}
		return writeResponse(approval.Response{RequestID: request.ID, Decision: approval.DecisionDeny, Reason: "approval request timed out"})
	}
	return writeResponse(response)
}

func writeApprovalHookFailure(err error) error {
	response := approval.Response{Decision: approval.DecisionDeny, Reason: "approval denied: " + err.Error()}
	_ = writeApprovalHookResponse(response)
	return err
}

func writeApprovalHookResponse(response approval.Response) error {
	decision := response.Decision
	if decision != approval.DecisionAllow {
		decision = approval.DecisionDeny
	}
	decisionPayload := map[string]any{"behavior": string(decision), "message": response.Reason}
	if len(response.UpdatedInput) > 0 {
		var updatedInput any
		if err := json.Unmarshal(response.UpdatedInput, &updatedInput); err != nil {
			return err
		}
		decisionPayload["updatedInput"] = updatedInput
	}
	output := map[string]any{
		"requestId": response.RequestID,
		"decision":  decision,
		"reason":    response.Reason,
		"hookSpecificOutput": map[string]any{
			"hookEventName": "PermissionRequest",
			"decision":      decisionPayload,
		},
	}
	return json.NewEncoder(os.Stdout).Encode(output)
}

func writeQuestionHookFailure(err error) error {
	response := approval.Response{Decision: approval.DecisionDeny, Reason: "approval denied: " + err.Error()}
	_ = writeQuestionHookResponse(response)
	return err
}

func writeQuestionHookResponse(response approval.Response) error {
	decision := "deny"
	if response.Decision == approval.DecisionAllow {
		decision = "allow"
	}
	output := map[string]any{
		"requestId": response.RequestID,
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       decision,
			"permissionDecisionReason": response.Reason,
		},
	}
	if len(response.UpdatedInput) > 0 && response.Decision == approval.DecisionAllow {
		var updatedInput any
		if err := json.Unmarshal(response.UpdatedInput, &updatedInput); err != nil {
			return err
		}
		output["hookSpecificOutput"].(map[string]any)["updatedInput"] = updatedInput
	}
	return json.NewEncoder(os.Stdout).Encode(output)
}

func listApprovals(store *approval.Store, args []string) error {
	positional := stripFlags(args)
	if len(positional) > 1 {
		return fmt.Errorf("usage: agenthail approval list [count] [--json]")
	}
	limit := 50
	if len(positional) == 1 {
		parsed, err := strconv.Atoi(positional[0])
		if err != nil || parsed < 1 {
			return fmt.Errorf("count must be a positive integer")
		}
		limit = parsed
	}
	rows, err := store.List(limit)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"approvals": rows})
	}
	if len(rows) == 0 {
		fmt.Println("(no approval requests)")
		return nil
	}
	for _, row := range rows {
		fmt.Printf("%s %-7s %-16s %s", row.ID, row.Decision, row.Tool, row.Summary)
		if len(row.Input) > 0 {
			fmt.Printf(" input=%s", row.Input)
		}
		if row.SessionID != "" {
			fmt.Printf(" session=%s", row.SessionID)
		}
		if row.DecisionReason != "" {
			fmt.Printf(" reason=%s", row.DecisionReason)
		}
		fmt.Println()
	}
	return nil
}

func decideApproval(store *approval.Store, decision approval.Decision, args []string) error {
	if hasFlag(args, "--json") {
		return fmt.Errorf("--json is only supported with approval list")
	}
	if len(args) < 1 || len(args) > 2 || strings.HasPrefix(args[0], "--") {
		return fmt.Errorf("usage: agenthail approval %s <request-id> [reason]", decision)
	}
	reason := "approved locally"
	if decision == approval.DecisionDeny {
		reason = "denied locally"
	}
	if len(args) == 2 {
		reason = strings.TrimSpace(args[1])
		if reason == "" {
			return fmt.Errorf("approval reason is empty")
		}
	}
	if err := store.Decide(args[0], decision, reason, time.Now()); err != nil {
		return err
	}
	fmt.Printf("approval %s: %s\n", decision, args[0])
	return nil
}

func answerApproval(store *approval.Store, args []string) error {
	if len(args) != 2 || strings.HasPrefix(args[0], "--") {
		return fmt.Errorf("usage: agenthail approval answer <request-id> '{\"questions\":[],\"answers\":{}}'")
	}
	input := []byte(args[1])
	if args[1] == "-" {
		var err error
		input, err = io.ReadAll(io.LimitReader(os.Stdin, maxApprovalHookBytes))
		if err != nil {
			return fmt.Errorf("read approval answer: %w", err)
		}
	}
	if err := store.DecideWithInput(args[0], approval.DecisionAllow, "answered locally", input, time.Now()); err != nil {
		return err
	}
	fmt.Printf("approval answer: %s\n", args[0])
	return nil
}
