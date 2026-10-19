package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

type claudeBackground struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	PID       int    `json:"pid"`
}

func (c *Claude) backgroundCommand(ctx context.Context, cwd string, args ...string) ([]byte, error) {
	binary, err := surface.ClaudeBinary(c.home)
	if err != nil {
		return nil, err
	}
	cmd := processGroupCommand(ctx, binary, args...)
	cmd.WaitDelay = time.Second
	cmd.Dir = cwd
	cmd.Env = claudeCommandEnv(c.home)
	return cmd.CombinedOutput()
}

// cmux's claude wrapper keys on CMUX_SURFACE_ID to inject a temporary
// --settings file that background jobs persist and replay on resume, after
// the file is gone. Without CMUX_ variables the wrapper passes through.
func claudeCommandEnv(home string) []string {
	var env []string
	for _, entry := range surface.AgentEnv(os.Environ()) {
		if !strings.HasPrefix(entry, "CMUX_") {
			env = append(env, entry)
		}
	}
	return append(env, "HOME="+home)
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
func validateClaudeStart(options surface.SessionStartOptions) error {
	if strings.TrimSpace(options.Message) == "" {
		return fmt.Errorf("message is required")
	}
	if options.ApprovalPolicy != "" {
		return fmt.Errorf("Claude uses permissionMode, not Codex approvalPolicy")
	}
	if options.Mode != "" || options.ServiceTier != "" || len(options.OutputSchema) > 0 {
		return fmt.Errorf("Claude creation does not support Codex turn options")
	}
	if options.Effort != "" && !slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, options.Effort) {
		return fmt.Errorf("invalid Claude effort")
	}
	if options.PermissionMode != "" && !slices.Contains([]string{"acceptEdits", "auto", "manual", "dontAsk", "plan"}, options.PermissionMode) {
		return fmt.Errorf("invalid Claude permission mode")
	}
	if options.Cwd == "" || !filepath.IsAbs(options.Cwd) {
		return fmt.Errorf("absolute cwd is required")
	}
	return nil
}

// StartSession opens a new conversation in the Claude host and sends the
// message as its first turn. The host answers the session's permission
// prompts, and the session has no Remote Control. The conversation id is
// chosen here, so an open whose outcome is unknown still names the session.
func (c *Claude) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	if err := validateClaudeStart(options); err != nil {
		return nil, nil, err
	}
	id := uuid.NewString()
	opened, err := c.host.client.Open(ctx, claudehost.OpenRequest{
		SessionID: id, Cwd: options.Cwd, Model: options.Model, PermissionMode: options.PermissionMode,
		Effort: options.Effort, Agent: options.Agent, Name: options.Name, Worktree: options.Worktree,
	})
	session := &surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: opened.PID, Name: options.Name, Cwd: options.Cwd, Status: surface.StatusIdle, Source: "agenthail", ConfiguredModel: options.Model, LastActive: time.Now()}
	if err != nil {
		var unknown *claudehost.OutcomeUnknownError
		if errors.As(err, &unknown) {
			return session, nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude creation: %w", err))
		}
		return nil, nil, fmt.Errorf("Claude creation: %w", err)
	}
	sent, err := c.sendHostTurn(ctx, session, options.Message, "")
	return session, sent, err
}

// claudeBackgroundStarter starts a conversation as a Claude Code background
// job, which registers Remote Control. It serves the claude-bg launcher only.
type claudeBackgroundStarter struct{ c *Claude }

func (c *Claude) BackgroundStarter() surface.SessionStarter { return claudeBackgroundStarter{c} }

func (s claudeBackgroundStarter) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	c := s.c
	if err := validateClaudeStart(options); err != nil {
		return nil, nil, err
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
					session := &surface.Session{ID: record.SessionID, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: record.Name, Cwd: record.Cwd, Status: surface.StatusUnknown, HasLocal: true, Source: "agenthail", ConfiguredModel: options.Model, LastActive: time.Now()}
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
	if claudeHosted(session) {
		return c.hostSessionAction(ctx, session, action)
	}
	records, err := c.backgroundSessions(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.Kind != "background" || record.SessionID != session.ID {
			continue
		}
		if action == "status" {
			return map[string]any{"id": record.ID, "sessionId": record.SessionID, "state": record.State}, nil
		}
		if action == "resume" {
			target := surface.Session{ID: record.SessionID, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: record.Cwd}
			resumed, err := c.reach(ctx, &target)
			if err != nil {
				return nil, err
			}
			return map[string]any{"id": record.ID, "sessionId": record.SessionID, "action": action, "unchanged": !resumed, "state": string(target.Status)}, nil
		}
		output, err := c.backgroundCommand(ctx, record.Cwd, action, record.ID)
		if err != nil {
			if action != "logs" {
				return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude %s: %w: %s", action, err, string(output)))
			}
			return nil, err
		}
		return map[string]any{"id": record.ID, "sessionId": record.SessionID, "action": action, "output": string(output)}, nil
	}
	return nil, fmt.Errorf("session is not a Claude background agent")
}

// hostSessionAction runs a lifecycle action on a session the Claude host
// runs. Stop closes it in the host; the conversation resumes on the next
// message or resume. The host keeps no background log.
func (c *Claude) hostSessionAction(ctx context.Context, session *surface.Session, action string) (map[string]any, error) {
	switch action {
	case "status":
		listing, err := c.host.client.List(ctx)
		if err != nil {
			return nil, surface.DeliveryUnavailable(err)
		}
		for _, hosted := range listing.Sessions {
			if hosted.SessionID == session.ID {
				return map[string]any{"sessionId": session.ID, "state": hosted.State, "pid": hosted.PID}, nil
			}
		}
		return map[string]any{"sessionId": session.ID, "state": "closed"}, nil
	case "stop":
		err := c.host.client.Close(ctx, session.ID)
		if claudehost.IsCode(err, "not_open") {
			return map[string]any{"sessionId": session.ID, "action": action, "unchanged": true}, nil
		}
		if err != nil {
			return nil, claudeHostDeliveryError(err)
		}
		return map[string]any{"sessionId": session.ID, "action": action}, nil
	case "resume":
		target := *session
		resumed, err := c.reach(ctx, &target)
		if err != nil {
			return nil, err
		}
		return map[string]any{"sessionId": session.ID, "action": action, "unchanged": !resumed, "state": string(target.Status)}, nil
	default:
		return nil, fmt.Errorf("a Claude host session has no background log; read its transcript with agenthail last")
	}
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
