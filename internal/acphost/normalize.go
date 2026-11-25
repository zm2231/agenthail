package acphost

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/acp"
	"github.com/zm2231/agenthail/internal/surface"
)

const textFlushInterval = time.Second

type toolCall struct {
	itemID   string
	title    string
	kind     string
	status   string
	rawInput json.RawMessage
	diffs    []acp.ToolCallContent
}

type normalizer struct {
	out      *transcriptWriter
	turnID   string
	run      *surface.TimelineItem
	runAt    time.Time
	runs     int
	calls    map[string]*toolCall
	planned  bool
	notice   func(string)
	writeErr error
}

func newNormalizer(out *transcriptWriter) *normalizer {
	return &normalizer{out: out, calls: map[string]*toolCall{}}
}

func (n *normalizer) emit(item surface.TimelineItem) {
	if item.TurnID == "" {
		item.TurnID = n.turnID
	}
	if item.Timestamp == "" {
		item.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := n.out.item(item); err != nil && n.writeErr == nil {
		n.writeErr = err
	}
}

func (n *normalizer) startTurn(turnID string, prompt surface.TimelineItem) {
	n.turnID, n.run, n.runs, n.calls, n.planned = turnID, nil, 0, map[string]*toolCall{}, false
	prompt.ID, prompt.Kind, prompt.Role = turnID+":user", "message", "user"
	if prompt.Title == "" {
		prompt.Title = "user"
	}
	n.emit(prompt)
}

func (n *normalizer) flushRun() {
	if n.run != nil {
		n.emit(*n.run)
		n.runAt = time.Now()
	}
}

func (n *normalizer) endRun() {
	n.flushRun()
	n.run = nil
}

func (n *normalizer) chunk(kind, role, text string) {
	if text == "" {
		return
	}
	if n.run == nil || n.run.Kind != kind {
		n.endRun()
		n.runs++
		prefix := "a"
		if kind == "reasoning" {
			prefix = "r"
		}
		n.run = &surface.TimelineItem{ID: fmt.Sprintf("%s:%s%d", n.turnID, prefix, n.runs), Kind: kind, Role: role, Title: role}
		n.runAt = time.Time{}
	}
	n.run.Text += text
	if time.Since(n.runAt) >= textFlushInterval {
		n.flushRun()
	}
}

func (n *normalizer) update(u acp.Update) {
	switch u.SessionUpdate {
	case "agent_message_chunk":
		n.chunk("message", "assistant", u.Text())
	case "agent_thought_chunk":
		n.chunk("reasoning", "assistant", u.Text())
	case "tool_call", "tool_call_update":
		n.endRun()
		n.tool(u)
	case "plan":
		n.endRun()
		lines := make([]string, 0, len(u.Entries))
		for _, entry := range u.Entries {
			lines = append(lines, fmt.Sprintf("[%s] %s", entry.Status, entry.Content))
		}
		n.emit(surface.TimelineItem{ID: n.turnID + ":plan", Kind: "event", Title: "Plan", Text: strings.Join(lines, "\n")})
	}
}

func (n *normalizer) tool(u acp.Update) {
	call := n.calls[u.ToolCallID]
	if call == nil {
		call = &toolCall{itemID: n.turnID + ":tool:" + u.ToolCallID}
		n.calls[u.ToolCallID] = call
	}
	if u.Title != nil {
		call.title = *u.Title
	}
	if u.Kind != "" {
		call.kind = u.Kind
	}
	if u.Status != "" {
		call.status = u.Status
	}
	if len(u.RawInput) > 0 {
		call.rawInput = u.RawInput
	}
	content := u.ToolContent()
	for _, part := range content {
		if part.Type == "diff" {
			call.diffs = append(call.diffs, part)
		}
	}
	title := call.title
	if title == "" {
		title = call.kind
	}
	n.emit(surface.TimelineItem{ID: call.itemID, Kind: "toolCall", Title: title, Text: string(call.rawInput), CallID: call.itemID, Status: call.status})
	if call.status != "completed" && call.status != "failed" {
		return
	}
	var parts []string
	for _, part := range content {
		switch {
		case part.Type == "content" && part.Content != nil:
			parts = append(parts, part.Content.Text)
		case part.Type == "diff":
			parts = append(parts, "Changed "+part.Path)
		}
	}
	if len(parts) == 0 && len(u.RawOutput) > 0 {
		parts = append(parts, string(u.RawOutput))
	}
	if len(parts) > 0 {
		n.emit(surface.TimelineItem{ID: call.itemID + ":result", Kind: "toolResult", Title: title, Text: strings.Join(parts, "\n"), CallID: call.itemID, Status: call.status})
	}
}

func (n *normalizer) noticeItem(text string) {
	n.endRun()
	n.emit(surface.TimelineItem{ID: fmt.Sprintf("%s:notice:%d", n.turnID, time.Now().UnixNano()), Kind: "notice", Title: "Agenthail", Text: text})
}

func (n *normalizer) endTurn(stop string, failure string) {
	n.endRun()
	status := "completed"
	switch {
	case failure != "":
		status = "failed"
		n.emit(surface.TimelineItem{ID: n.turnID + ":failure", Kind: "notice", Title: "Turn failed", Text: failure, Status: "failed"})
	case stop == acp.StopCancelled:
		status = "interrupted"
	case stop != acp.StopEndTurn:
		status = "failed"
		n.emit(surface.TimelineItem{ID: n.turnID + ":stop", Kind: "notice", Title: "Turn stopped", Text: stopReasonText(stop), Status: "failed"})
	}
	n.emit(surface.TimelineItem{ID: n.turnID + ":done", Kind: "done", Title: "done", Status: status})
	n.turnID = ""
}

func stopReasonText(stop string) string {
	switch stop {
	case acp.StopMaxTokens:
		return "The agent stopped: it reached its token limit."
	case acp.StopMaxTurnRequests:
		return "The agent stopped: it reached its limit of requests in one turn."
	case acp.StopRefusal:
		return "The agent refused to continue."
	}
	return "The agent stopped: " + stop
}

func (n *normalizer) request(id string) (Request, bool) {
	call := n.calls[id]
	if call == nil {
		return Request{ToolCallID: id}, false
	}
	return Request{ToolCallID: id, Title: call.title, Kind: call.kind, RawInput: call.rawInput, Diffs: call.diffs}, true
}
