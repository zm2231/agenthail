package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var claudeControlPoll = 250 * time.Millisecond

type claudeRemoteEvent struct {
	UUID     string `json:"uuid"`
	Type     string `json:"type"`
	Response struct {
		RequestID string `json:"request_id"`
		Subtype   string `json:"subtype"`
		Error     string `json:"error"`
	} `json:"response"`
}

func (c *Claude) remoteRequest(ctx context.Context, sess *surface.Session, method, path, body string) (string, error) {
	cookies, err := c.cookieSource(ctx, sess)
	if err != nil {
		return "", cookieSourceError(err)
	}
	request := c.request
	if request == nil {
		request = claudeSendRequest
	}
	headers := c.headerMap("", sess.Bridge)
	if body != "" {
		headers["content-type"] = "application/json"
	}
	status, data, err := request(ctx, method, "https://claude.ai"+path, headers, body, cookies, 15*time.Second)
	if err != nil {
		return "", err
	}
	if status == http.StatusOK {
		return data, nil
	}
	err = fmt.Errorf("Claude control %s returned HTTP %d: %s", method, status, diagnosticExcerpt(data))
	switch status {
	case http.StatusBadRequest:
		return "", surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	case http.StatusUnauthorized:
		return "", surface.DeliveryTerminal(err, surface.DeliveryAuthenticationNeeded)
	case http.StatusForbidden:
		return "", surface.DeliveryTerminal(err, surface.DeliveryAccessDenied)
	case http.StatusNotFound:
		return "", surface.DeliveryTerminal(err, surface.DeliveryTargetMissing)
	}
	return "", err
}

// remoteState proves that Remote Control is connected to this exact session
// and returns the model its runtime reports. A bridge id in the local record
// only says the session was attached at some point.
func (c *Claude) remoteState(ctx context.Context, sess *surface.Session) (string, error) {
	data, err := c.remoteRequest(ctx, sess, "GET", "/v1/code/sessions/"+toCse(sess.Bridge), "")
	if err != nil {
		return "", err
	}
	type state struct {
		ID         string `json:"id"`
		Connection string `json:"connection_status"`
		Metadata   struct {
			Model string `json:"model"`
		} `json:"external_metadata"`
	}
	var response struct {
		Shape   *state `json:"response_shape"`
		Session *state `json:"session"`
	}
	if err := json.Unmarshal([]byte(data), &response); err != nil {
		return "", fmt.Errorf("parse Claude Remote Control state: %w", err)
	}
	current := response.Shape
	if current == nil {
		current = response.Session
	}
	if current == nil || current.ID != toCse(sess.Bridge) || current.Connection != "connected" {
		return "", surface.DeliveryUnavailable(fmt.Errorf("Claude Remote Control is not connected to %s", sess.ID))
	}
	return current.Metadata.Model, nil
}

func (c *Claude) remoteEvents(ctx context.Context, sess *surface.Session, after string) ([]claudeRemoteEvent, string, bool, error) {
	id := "session_" + strings.TrimPrefix(toCse(sess.Bridge), "cse_")
	data, err := c.remoteRequest(ctx, sess, "GET", "/v1/sessions/"+id+"/events?limit=100&after_id="+url.QueryEscape(after), "")
	if err != nil {
		return nil, "", false, err
	}
	var page struct {
		Data   []claudeRemoteEvent `json:"data"`
		LastID string              `json:"last_id"`
		More   bool                `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(data), &page); err != nil {
		return nil, "", false, fmt.Errorf("parse Claude Remote Control events: %w", err)
	}
	return page.Data, page.LastID, page.More, nil
}

func (c *Claude) Control(ctx context.Context, sess *surface.Session, control surface.ControlRequest) (*surface.ControlResult, error) {
	value := strings.TrimSpace(control.Value)
	if err := validateClaudeControl(control.Action, value); err != nil {
		return nil, err
	}
	if err := c.EnsureWritable(ctx, sess); err != nil {
		return nil, err
	}
	if claudeHosted(sess) {
		return c.hostControl(ctx, sess, control.Action, value)
	}
	if control.Action == "permission-mode" {
		return nil, fmt.Errorf("%w: only a session the Claude host runs changes its permission mode here", surface.ErrUnsupported)
	}
	if c.localOnly || nativeClaudeOnly(sess) {
		return nil, surface.ErrUnsupported
	}
	if err := c.reachRunning(ctx, sess); err != nil {
		return nil, err
	}
	model, err := c.remoteState(ctx, sess)
	if err != nil {
		return nil, err
	}
	switch control.Action {
	case "status":
		return &surface.ControlResult{SessionID: sess.ID, Action: "status", Evidence: surface.EvidenceReplyObserved, Source: "claude-runtime-state", Output: model, Detail: "Remote Control is connected to this session; the model comes from its runtime"}, nil
	case "compact":
		return c.compactControl(ctx, sess)
	case "context", "usage", "effort", "rename":
		return c.commandControl(ctx, sess, control.Action, value)
	case "model":
		result, err := c.controlRequest(ctx, sess, "model", map[string]any{"subtype": "set_model", "model": value})
		if err == nil {
			result.Output = value
			sess.ConfiguredModel = value
		}
		return result, err
	}
	observation, err := c.Observe(ctx, sess)
	if err != nil {
		return nil, err
	}
	if observation == nil || observation.Status != surface.StatusBusy {
		if control.Action == "steer" {
			return nil, surface.DeliveryUnavailable(fmt.Errorf("%w (use 'send' instead)", surface.ErrNothingToSteer))
		}
		return nil, fmt.Errorf("session idle; nothing to interrupt")
	}
	if control.Action == "steer" {
		sent, err := c.postMessage(ctx, sess, value)
		if err != nil {
			return nil, err
		}
		return &surface.ControlResult{SessionID: sess.ID, RequestID: sent.UUID, TurnID: sent.UUID, Action: "steer", Evidence: surface.EvidenceTransportAccepted, Source: "claude-remote-control", Detail: "Steer accepted; Claude has not confirmed it reached the current turn."}, nil
	}
	result, err := c.controlRequest(ctx, sess, "interrupt", map[string]any{"subtype": "interrupt"})
	if err != nil {
		return result, err
	}
	result.TurnID = observation.ActiveTurnID
	return c.confirmInterrupt(ctx, sess, result)
}

func validateClaudeControl(action, value string) error {
	switch action {
	case "status", "compact", "interrupt", "context", "usage":
		if value != "" {
			return fmt.Errorf("%s takes no value", action)
		}
	case "model":
		if value == "" || strings.ContainsAny(value, "\r\n\t ") {
			return fmt.Errorf("one explicit model is required")
		}
	case "permission-mode":
		if !validClaudePermissionMode(value) {
			return fmt.Errorf("permission mode must be one of %s", strings.Join(claudePermissionModes, ", "))
		}
	case "steer":
		if value == "" || strings.HasPrefix(value, "/") {
			return fmt.Errorf("steer requires a message, not a slash command")
		}
	case "rename":
		if value == "" || len(value) > 255 || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("rename requires one line of up to 255 bytes")
		}
	case "effort":
		switch value {
		case "low", "medium", "high", "max", "auto":
		default:
			return fmt.Errorf("effort requires low, medium, high, max or auto")
		}
	default:
		return surface.ErrUnsupported
	}
	return nil
}

// hostControl keeps the Claude host's own interrupt, steer, model and
// permission mode. The host returns once the SDK acknowledges the change or
// takes the turn.
func (c *Claude) hostControl(ctx context.Context, sess *surface.Session, action, value string) (*surface.ControlResult, error) {
	switch action {
	case "model", "permission-mode":
		return c.hostSetting(ctx, sess, action, value)
	case "interrupt":
		if err := c.interruptHost(ctx, sess); err != nil {
			return nil, err
		}
		return &surface.ControlResult{SessionID: sess.ID, Action: action, Evidence: surface.EvidenceDelivered, Source: "claude-host"}, nil
	case "steer":
		sent, err := c.sendHostTurn(ctx, sess, value, "now", nil)
		if err != nil {
			return nil, err
		}
		return &surface.ControlResult{SessionID: sess.ID, RequestID: sent.UUID, TurnID: sent.UUID, Action: action, Evidence: surface.EvidenceDelivered, Source: "claude-host"}, nil
	}
	return nil, surface.ErrUnsupported
}

// controlRequest posts a native control_request and waits for the
// control_response carrying its request id. An accepted request with no
// matching response is an unknown outcome, not a rejection.
func (c *Claude) controlRequest(ctx context.Context, sess *surface.Session, action string, request map[string]any) (*surface.ControlResult, error) {
	uuid, requestID := newUUID(), "agenthail-"+newUUID()
	body, _ := json.Marshal(map[string]any{"events": []any{map[string]any{"payload": map[string]any{"type": "control_request", "uuid": uuid, "request_id": requestID, "request": request}}}})
	result := &surface.ControlResult{SessionID: sess.ID, RequestID: requestID, Action: action, Evidence: surface.EvidenceUnknown, Source: "claude-control-response"}
	if _, err := c.remoteRequest(ctx, sess, "POST", "/v1/code/sessions/"+toCse(sess.Bridge)+"/events", string(body)); err != nil {
		if surface.IsDeliveryTerminal(err) {
			result.Evidence = surface.EvidenceFailed
			return result, err
		}
		return result, surface.DeliveryOutcomeUnknown(err)
	}
	result.Evidence = surface.EvidenceTransportAccepted
	after := uuid
	ticker := time.NewTicker(claudeControlPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result.Evidence = surface.EvidenceUnknown
			return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude %s was accepted but no matching control response arrived: %w", action, ctx.Err()))
		case <-ticker.C:
		}
		events, last, more, err := c.remoteEvents(ctx, sess, after)
		if err != nil {
			result.Evidence = surface.EvidenceUnknown
			return result, surface.DeliveryOutcomeUnknown(err)
		}
		for _, event := range events {
			if event.Type != "control_response" || event.Response.RequestID != requestID {
				continue
			}
			switch event.Response.Subtype {
			case "error":
				result.Evidence = surface.EvidenceFailed
				return result, fmt.Errorf("Claude rejected %s: %s", action, event.Response.Error)
			case "success":
				result.Evidence = surface.EvidenceDelivered
				return result, nil
			}
		}
		if len(events) > 0 {
			if last == "" || last == after {
				result.Evidence = surface.EvidenceUnknown
				return result, surface.DeliveryOutcomeUnknown(errors.New("Claude Remote Control event cursor did not advance"))
			}
			after = last
		} else if more {
			result.Evidence = surface.EvidenceUnknown
			return result, surface.DeliveryOutcomeUnknown(errors.New("Claude Remote Control event page is incomplete"))
		}
	}
}

// confirmInterrupt waits for the turn that was active to end. A turn that
// finished on its own also ends, so this proves the turn stopped, not that the
// interrupt stopped it.
func (c *Claude) confirmInterrupt(ctx context.Context, sess *surface.Session, result *surface.ControlResult) (*surface.ControlResult, error) {
	ticker := time.NewTicker(claudeControlPoll)
	defer ticker.Stop()
	for {
		observation, err := c.Observe(ctx, sess)
		if err == nil && observation != nil && (observation.Status != surface.StatusBusy || result.TurnID != "" && observation.ActiveTurnID != result.TurnID) {
			result.Source = "claude-control-response+transcript"
			result.Detail = "Claude acknowledged the interrupt and the active turn ended."
			return result, nil
		}
		select {
		case <-ctx.Done():
			result.Evidence = surface.EvidenceUnknown
			result.Detail = "Claude acknowledged the interrupt; the active turn was not seen ending."
			return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("%s: %w", result.Detail, ctx.Err()))
		case <-ticker.C:
		}
	}
}

func (c *Claude) compactControl(ctx context.Context, sess *surface.Session) (*surface.ControlResult, error) {
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("compact confirmation requires a local transcript: %w", err)
	}
	sent, err := c.postMessage(ctx, sess, "/compact")
	if err != nil {
		return nil, err
	}
	result := &surface.ControlResult{SessionID: sess.ID, RequestID: sent.UUID, Action: "compact", Evidence: surface.EvidenceTransportAccepted, Source: "claude-compact-boundary"}
	ticker := time.NewTicker(claudeControlPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result.Evidence = surface.EvidenceUnknown
			return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("compact was accepted but completion was not observed: %w", ctx.Err()))
		case <-ticker.C:
		}
		completed, err := readClaudeCompactCompletion(path, info.Size(), sent.UUID)
		if err != nil {
			result.Evidence = surface.EvidenceUnknown
			return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("read compact completion: %w", err))
		}
		if completed {
			result.Evidence = surface.EvidenceDelivered
			result.Detail = "A new compact boundary followed this request."
			return result, nil
		}
	}
}

// commandControl runs one allowlisted slash command and returns its output.
// The output must be joined to this request: by its parent record, or by a
// cloud event that follows the request. Another prompt arriving first makes
// the join ambiguous, so the outcome is unknown.
func (c *Claude) commandControl(ctx context.Context, sess *surface.Session, action, value string) (*surface.ControlResult, error) {
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("command confirmation requires a local transcript: %w", err)
	}
	command := "/" + action
	if value != "" {
		command += " " + value
	}
	sent, err := c.postMessage(ctx, sess, command)
	if err != nil {
		return nil, err
	}
	result := &surface.ControlResult{SessionID: sess.ID, RequestID: sent.UUID, Action: action, Evidence: surface.EvidenceTransportAccepted, Source: "claude-command-transcript"}
	offset := info.Size()
	type answer struct {
		output string
		failed bool
	}
	candidates := map[string]answer{}
	failed := false
	cloudEvents := map[string]bool{}
	after := sent.UUID
	ticker := time.NewTicker(claudeControlPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result.Evidence = surface.EvidenceUnknown
			return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude /%s was accepted but its output was not observed: %w", action, ctx.Err()))
		case <-ticker.C:
		}
		current, err := os.Stat(path)
		if err != nil || !os.SameFile(info, current) || current.Size() < offset {
			result.Evidence = surface.EvidenceUnknown
			return result, surface.DeliveryOutcomeUnknown(errors.New("command transcript was replaced or truncated"))
		}
		found := false
		offset, err = scanAppendedJSONL(ctx, path, offset, maxClaudeTranscriptRecordBytes, func(line []byte) error {
			var record claudeRecord
			if json.Unmarshal(line, &record) != nil || record.Type != "system" || record.Subtype != "local_command" || record.CommandRun == nil || record.CommandRun.Command != action || record.CommandRun.Args != value {
				return nil
			}
			output, stderr := claudeLocalCommandOutput(record.Content)
			candidates[record.UUID] = answer{output, stderr}
			if record.ParentUUID == sent.UUID {
				result.Output, failed = output, stderr
				found = true
			}
			return nil
		})
		if err != nil {
			result.Evidence = surface.EvidenceUnknown
			return result, surface.DeliveryOutcomeUnknown(err)
		}
		if !found {
			events, last, _, err := c.remoteEvents(ctx, sess, after)
			if err != nil {
				result.Evidence = surface.EvidenceUnknown
				return result, surface.DeliveryOutcomeUnknown(err)
			}
			for _, event := range events {
				if event.Type == "user" && event.UUID != sent.UUID {
					result.Evidence = surface.EvidenceUnknown
					return result, surface.DeliveryOutcomeUnknown(errors.New("another prompt arrived before the command output"))
				}
				if event.UUID != "" {
					cloudEvents[event.UUID] = true
				}
			}
			if len(events) > 0 {
				if last == "" || last == after {
					result.Evidence = surface.EvidenceUnknown
					return result, surface.DeliveryOutcomeUnknown(errors.New("Claude Remote Control event cursor did not advance"))
				}
				after = last
			}
			for uuid, candidate := range candidates {
				if cloudEvents[uuid] {
					result.Output, failed = candidate.output, candidate.failed
					result.Source = "claude-cloud-command+transcript"
					found = true
					break
				}
			}
		}
		if !found {
			continue
		}
		if failed {
			result.Evidence = surface.EvidenceFailed
			return result, fmt.Errorf("Claude rejected /%s: %s", action, result.Output)
		}
		result.Evidence = surface.EvidenceReplyObserved
		return result, nil
	}
}
