package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type claudeRemoteEvent struct {
	UUID    string `json:"uuid"`
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Model   string `json:"model"`
	Message struct {
		Content any `json:"content"`
	} `json:"message"`
	Response struct {
		RequestID string `json:"request_id"`
		Subtype   string `json:"subtype"`
		Error     string `json:"error"`
	} `json:"response"`
}

func (c *Claude) remoteRequest(ctx context.Context, method, path, body string) (string, error) {
	request := c.request
	if request == nil {
		request = claudeSendRequest
	}
	headers := c.headerMap("", "")
	headers["content-type"] = "application/json"
	status, data, err := request(ctx, method, "https://claude.ai"+path, headers, body, c.cookieBridge, "https://claude.ai/", 15*time.Second)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		err := fmt.Errorf("Claude control %s returned HTTP %d", method, status)
		switch status {
		case 400:
			return "", surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
		case 401:
			return "", surface.DeliveryTerminal(err, surface.DeliveryAuthenticationNeeded)
		case 403:
			return "", surface.DeliveryTerminal(err, surface.DeliveryAccessDenied)
		case 404:
			return "", surface.DeliveryTerminal(err, surface.DeliveryTargetMissing)
		}
		return "", err
	}
	return data, nil
}

func (c *Claude) remoteState(ctx context.Context, session *surface.Session) (string, error) {
	if nativeClaudeOnly(session) {
		return "", surface.ErrUnsupported
	}
	data, err := c.remoteRequest(ctx, "GET", "/v1/code/sessions/"+toCse(session.ID), "")
	if err != nil {
		return "", err
	}
	var response struct {
		State struct {
			ID         string `json:"id"`
			Connection string `json:"connection_status"`
			Metadata   struct {
				Model string `json:"model"`
			} `json:"external_metadata"`
		} `json:"response_shape"`
	}
	if err := json.Unmarshal([]byte(data), &response); err != nil {
		return "", fmt.Errorf("parse Claude control state: %w", err)
	}
	if response.State.ID != toCse(session.ID) || response.State.Connection != "connected" {
		return "", fmt.Errorf("Claude Remote Control is not connected to the exact target")
	}
	return response.State.Metadata.Model, nil
}

func (c *Claude) remoteEvents(ctx context.Context, session *surface.Session, after string) ([]claudeRemoteEvent, string, bool, error) {
	data, err := c.remoteRequest(ctx, "GET", "/v1/sessions/"+strings.Replace(toCse(session.ID), "cse_", "session_", 1)+"/events?limit=100&after_id="+url.QueryEscape(after), "")
	if err != nil {
		return nil, "", false, err
	}
	var page struct {
		Data   []claudeRemoteEvent `json:"data"`
		LastID string              `json:"last_id"`
		More   bool                `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(data), &page); err != nil {
		return nil, "", false, fmt.Errorf("parse Claude control events: %w", err)
	}
	return page.Data, page.LastID, page.More, nil
}

func (c *Claude) Control(ctx context.Context, session *surface.Session, control surface.ControlRequest) (*surface.ControlResult, error) {
	value := strings.TrimSpace(control.Value)
	var subtype string
	switch control.Action {
	case "compact":
		if value != "" {
			return nil, fmt.Errorf("compact takes no value")
		}
		return c.compactControl(ctx, session)
	case "status":
		if value != "" {
			return nil, fmt.Errorf("status takes no value")
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		model, err := c.remoteState(ctx, session)
		if err != nil {
			return nil, err
		}
		return &surface.ControlResult{SessionID: session.ID, Action: "status", Evidence: surface.EvidenceReplyObserved, Source: "claude-runtime-state", Output: model, Detail: "Remote Control connected to the exact target; model from runtime metadata"}, nil
	case "model":
		if value == "" || strings.ContainsAny(value, "\r\n\t ") {
			return nil, fmt.Errorf("one explicit model is required")
		}
		subtype = "set_model"
	case "interrupt":
		if value != "" {
			return nil, fmt.Errorf("interrupt takes no value")
		}
		subtype = "interrupt"
	case "steer":
		if value == "" || strings.HasPrefix(value, "/") {
			return nil, fmt.Errorf("steer requires a message, not a slash command")
		}
	case "context", "usage", "rename", "effort":
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("control value must be one line")
		}
		if (control.Action == "context" || control.Action == "usage") && value != "" {
			return nil, fmt.Errorf("%s takes no value", control.Action)
		}
		if control.Action == "rename" && (value == "" || len(value) > 255) {
			return nil, fmt.Errorf("rename requires an explicit name up to 255 bytes")
		}
		if control.Action == "effort" && value != "low" && value != "medium" && value != "high" && value != "max" && value != "auto" {
			return nil, fmt.Errorf("effort requires low, medium, high, max or auto")
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return c.commandControl(ctx, session, control.Action, value)
	default:
		return nil, surface.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := c.remoteState(ctx, session); err != nil {
		return nil, err
	}
	activeTurnID := ""
	if control.Action == "steer" || control.Action == "interrupt" {
		observation, err := c.Observe(ctx, session)
		if err != nil {
			return nil, err
		}
		if observation == nil || observation.ActiveTurnID == "" {
			return nil, fmt.Errorf("session idle; nothing to %s", control.Action)
		}
		activeTurnID = observation.ActiveTurnID
		if control.Action == "steer" {
			sent, err := c.postMessage(ctx, session, value)
			if err != nil {
				return nil, err
			}
			return &surface.ControlResult{SessionID: session.ID, RequestID: sent.UUID, TurnID: sent.UUID, Action: "steer", Evidence: surface.EvidenceTransportAccepted, Source: "claude-remote-control", Detail: "accepted; application to the active turn is not confirmed"}, nil
		}
	}
	uuid, requestID := newUUID(), "agenthail-"+newUUID()
	request := map[string]any{"subtype": subtype}
	if subtype == "set_model" {
		request["model"] = value
	}
	body, _ := json.Marshal(map[string]any{"events": []any{map[string]any{"payload": map[string]any{"type": "control_request", "uuid": uuid, "request_id": requestID, "request": request}}}})
	result := &surface.ControlResult{SessionID: session.ID, RequestID: requestID, Action: control.Action, Evidence: surface.EvidenceUnknown, Source: "claude-control-response"}
	result.TurnID = activeTurnID
	if _, err := c.remoteRequest(ctx, "POST", "/v1/code/sessions/"+toCse(session.ID)+"/events", string(body)); err != nil {
		if surface.IsDeliveryTerminal(err) {
			result.Evidence = surface.EvidenceFailed
			return result, err
		}
		return result, surface.DeliveryOutcomeUnknown(err)
	}
	result.Evidence = surface.EvidenceTransportAccepted
	after := uuid
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result.Evidence = surface.EvidenceUnknown
			return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude %s accepted but no matching control response: %w", control.Action, ctx.Err()))
		case <-ticker.C:
			events, last, more, err := c.remoteEvents(ctx, session, after)
			if err != nil {
				result.Evidence = surface.EvidenceUnknown
				return result, surface.DeliveryOutcomeUnknown(err)
			}
			for _, event := range events {
				if event.Type != "control_response" || event.Response.RequestID != requestID {
					continue
				}
				if event.Response.Subtype == "error" {
					result.Evidence = surface.EvidenceFailed
					return result, fmt.Errorf("Claude %s rejected: %s", control.Action, event.Response.Error)
				}
				if event.Response.Subtype != "success" {
					continue
				}
				result.Evidence = surface.EvidenceDelivered
				result.Output = value
				if control.Action == "interrupt" {
					return c.confirmInterrupt(ctx, session, result)
				}
				return result, nil
			}
			if len(events) > 0 {
				if last == "" || last == after {
					result.Evidence = surface.EvidenceUnknown
					return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude control event cursor did not advance"))
				}
				after = last
			} else if more {
				result.Evidence = surface.EvidenceUnknown
				return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude control event page is incomplete"))
			}
		}
	}
}

func (c *Claude) compactControl(ctx context.Context, session *surface.Session) (*surface.ControlResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := c.remoteState(ctx, session); err != nil {
		return nil, err
	}
	path := session.Transcript
	if path == "" {
		path = c.transcriptPath(session)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("compact confirmation requires a local transcript: %w", err)
	}
	sent, err := c.postMessage(ctx, session, "/compact")
	if err != nil {
		return nil, err
	}
	result := &surface.ControlResult{SessionID: session.ID, RequestID: sent.UUID, Action: "compact", Evidence: surface.EvidenceTransportAccepted, Source: "claude-compact-boundary"}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result.Evidence = surface.EvidenceUnknown
			return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("compact accepted but no new request-linked boundary: %w", ctx.Err()))
		case <-ticker.C:
			completed, err := readClaudeCompactCompletion(path, info.Size(), sent.UUID)
			if err != nil {
				result.Evidence = surface.EvidenceUnknown
				return result, surface.DeliveryOutcomeUnknown(err)
			}
			if completed {
				result.Evidence = surface.EvidenceDelivered
				result.Detail = "new compact boundary observed after the exact request"
				return result, nil
			}
		}
	}
}

func (c *Claude) commandControl(ctx context.Context, session *surface.Session, action, value string) (*surface.ControlResult, error) {
	if _, err := c.remoteState(ctx, session); err != nil {
		return nil, err
	}
	path := session.Transcript
	if path == "" {
		path = c.transcriptPath(session)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("command confirmation requires a local transcript: %w", err)
	}
	command := "/" + action
	if value != "" {
		command += " " + value
	}
	sent, err := c.postMessage(ctx, session, command)
	if err != nil {
		return nil, err
	}
	result := &surface.ControlResult{SessionID: session.ID, RequestID: sent.UUID, Action: action, Evidence: surface.EvidenceTransportAccepted, Source: "claude-command-transcript"}
	offset := info.Size()
	candidates := map[string]string{}
	cloudOutputs := map[string]bool{}
	after := sent.UUID
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result.Evidence = surface.EvidenceUnknown
			return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude /%s accepted but no request-linked command output: %w", action, ctx.Err()))
		case <-ticker.C:
			currentInfo, statErr := os.Stat(path)
			if statErr != nil || !os.SameFile(info, currentInfo) || currentInfo.Size() < offset {
				result.Evidence = surface.EvidenceUnknown
				return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("command transcript was replaced or truncated"))
			}
			found := false
			next, err := scanAppendedJSONL(ctx, path, offset, maxClaudeTranscriptRecordBytes, func(line []byte) error {
				var record claudeRecord
				if json.Unmarshal(line, &record) != nil || record.Type != "system" || record.Subtype != "local_command" || record.CommandRun == nil || record.CommandRun.Command != action || record.CommandRun.Args != value {
					return nil
				}
				output := strings.TrimSpace(record.Content)
				output = strings.TrimPrefix(output, "<local-command-stdout>")
				output = strings.TrimSuffix(output, "</local-command-stdout>")
				output = strings.TrimSpace(ansiEscapeRe.ReplaceAllString(output, ""))
				candidates[record.UUID] = output
				if record.ParentUUID == sent.UUID {
					result.Output = output
					found = true
				}
				return nil
			})
			if err != nil {
				result.Evidence = surface.EvidenceUnknown
				return result, surface.DeliveryOutcomeUnknown(err)
			}
			offset = next
			if !found {
				events, last, _, readErr := c.remoteEvents(ctx, session, after)
				if readErr != nil {
					result.Evidence = surface.EvidenceUnknown
					return result, surface.DeliveryOutcomeUnknown(readErr)
				}
				for _, event := range events {
					if event.Type == "user" && event.UUID != sent.UUID {
						result.Evidence = surface.EvidenceUnknown
						return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("another prompt arrived before command confirmation"))
					}
					if event.UUID != "" {
						cloudOutputs[event.UUID] = true
					}
				}
				if len(events) > 0 {
					if last == "" || last == after {
						result.Evidence = surface.EvidenceUnknown
						return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude command event cursor did not advance"))
					}
					after = last
				}
				for uuid, output := range candidates {
					if cloudOutputs[uuid] {
						result.Output = output
						result.Source = "claude-cloud-command+transcript"
						found = true
						break
					}
				}
			}
			if found {
				if strings.Contains(result.Output, "<local-command-stderr>") {
					result.Output = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(result.Output, "<local-command-stderr>"), "</local-command-stderr>"))
					result.Evidence = surface.EvidenceFailed
					return result, fmt.Errorf("Claude /%s rejected: %s", action, result.Output)
				}
				result.Evidence = surface.EvidenceReplyObserved
				return result, nil
			}
		}
	}
}

func (c *Claude) confirmInterrupt(ctx context.Context, session *surface.Session, result *surface.ControlResult) (*surface.ControlResult, error) {
	path := session.Transcript
	if path == "" {
		path = c.transcriptPath(session)
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		turns, err := readClaudeTailTurns(ctx, path)
		if err == nil {
			for _, turn := range turns {
				if turn.UserID == result.TurnID && (turn.Interrupted || turn.Done) {
					result.Source = "claude-control-response+transcript"
					result.Detail = "interrupt acknowledged; original active turn is terminal"
					return result, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			result.Evidence = surface.EvidenceUnknown
			result.Detail = "interrupt acknowledged; original active-turn termination was not observed"
			return result, surface.DeliveryOutcomeUnknown(fmt.Errorf("%s: %w", result.Detail, ctx.Err()))
		case <-ticker.C:
		}
	}
}
