package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type claudeBackground struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
}

func (c *Claude) backgroundCommand(ctx context.Context, cwd string, args ...string) ([]byte, error) {
	binary, err := claudeBinary(c.home)
	if err != nil {
		return nil, err
	}
	cmd := processGroupCommand(ctx, binary, args...)
	cmd.WaitDelay = time.Second
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+c.home)
	return cmd.CombinedOutput()
}
func (c *Claude) backgroundSessions(ctx context.Context) ([]claudeBackground, error) {
	data, err := c.backgroundCommand(ctx, "", "agents", "--json", "--all")
	if err != nil {
		return nil, fmt.Errorf("Claude agents: %w: %s", err, string(data))
	}
	var records []claudeBackground
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("parse Claude agents: %w", err)
	}
	return records, nil
}
func (c *Claude) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	if strings.TrimSpace(options.Message) == "" {
		return nil, nil, fmt.Errorf("message is required")
	}
	if options.ApprovalPolicy != "" {
		return nil, nil, fmt.Errorf("Claude uses permissionMode, not Codex approvalPolicy")
	}
	if options.Mode != "" || options.ServiceTier != "" || len(options.OutputSchema) > 0 {
		return nil, nil, fmt.Errorf("Claude background creation does not support Codex turn options")
	}
	if options.Effort != "" && !slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, options.Effort) {
		return nil, nil, fmt.Errorf("invalid Claude effort")
	}
	if options.PermissionMode != "" && !slices.Contains([]string{"acceptEdits", "auto", "manual", "dontAsk", "plan"}, options.PermissionMode) {
		return nil, nil, fmt.Errorf("invalid Claude permission mode")
	}
	if options.Cwd == "" || !filepath.IsAbs(options.Cwd) {
		return nil, nil, fmt.Errorf("absolute cwd is required")
	}
	args := []string{"--bg"}
	for _, pair := range [][2]string{{"--model", options.Model}, {"--name", options.Name}, {"--worktree", options.Worktree}, {"--agent", options.Agent}, {"--permission-mode", options.PermissionMode}, {"--effort", options.Effort}} {
		if pair[1] != "" {
			args = append(args, pair[0], pair[1])
		}
	}
	args = append(args, "--", options.Message)
	data, err := c.backgroundCommand(ctx, options.Cwd, args...)
	if err != nil {
		return nil, nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude creation: %w: %s", err, string(data)))
	}
	shortID, parseErr := claudeBackgroundID(string(data))
	if parseErr != nil {
		return nil, nil, surface.DeliveryOutcomeUnknown(parseErr)
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		records, listErr := c.backgroundSessions(ctx)
		if listErr == nil {
			for _, record := range records {
				if record.ID == shortID && record.Kind == "background" && record.SessionID != "" {
					session := &surface.Session{ID: record.SessionID, Surface: surface.KindClaude, Name: record.Name, Cwd: record.Cwd, Status: surface.StatusUnknown, HasLocal: true, Source: "agenthail", LastActive: time.Now()}
					return session, nil, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, nil, surface.DeliveryOutcomeUnknown(ctx.Err())
		case <-deadline.C:
			return nil, nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude started but registration is not yet confirmed: %s", string(data)))
		case <-ticker.C:
		}
	}
}
func (c *Claude) SessionAction(ctx context.Context, session *surface.Session, action string) (map[string]any, error) {
	if action != "stop" && action != "resume" && action != "logs" && action != "status" {
		return nil, fmt.Errorf("Claude lifecycle action must be status, stop, resume, or logs")
	}
	records, err := c.backgroundSessions(ctx)
	if err != nil {
		return nil, err
	}
	localID := strings.TrimSuffix(filepath.Base(session.Transcript), ".jsonl")
	for _, record := range records {
		if record.Kind != "background" || (record.SessionID != session.ID && record.SessionID != localID) {
			continue
		}
		if action == "status" {
			return map[string]any{"id": record.ID, "sessionId": record.SessionID, "state": record.State}, nil
		}
		if action == "resume" && (record.State == "working" || record.State == "running" || record.State == "blocked" || record.State == "starting") {
			return map[string]any{"id": record.ID, "sessionId": record.SessionID, "action": action, "unchanged": true, "state": record.State}, nil
		}
		args := []string{action, record.ID}
		if action == "resume" {
			args = []string{"--bg", "--resume", record.SessionID}
		}
		output, err := c.backgroundCommand(ctx, record.Cwd, args...)
		if err != nil {
			if action != "logs" {
				return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude %s: %w: %s", action, err, string(output)))
			}
			return nil, err
		}
		if action == "resume" {
			resumedID, parseErr := claudeBackgroundID(string(output))
			if parseErr != nil || resumedID != record.ID {
				return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude resume identity was not confirmed; inspect claude agents: %s", string(output)))
			}
		}
		return map[string]any{"id": record.ID, "sessionId": record.SessionID, "action": action, "output": string(output)}, nil
	}
	return nil, fmt.Errorf("session is not a Claude background agent")
}

var claudeANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)
var claudeBackgroundLine = regexp.MustCompile(`(?m)^backgrounded · ([A-Za-z0-9_-]+)(?: |$)`)

func claudeBackgroundID(output string) (string, error) {
	matches := claudeBackgroundLine.FindStringSubmatch(claudeANSI.ReplaceAllString(output, ""))
	if len(matches) != 2 {
		return "", fmt.Errorf("Claude returned no background session ID; inspect claude agents before retrying")
	}
	return matches[1], nil
}
