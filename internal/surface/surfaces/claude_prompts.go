package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// ClaudePermissionPrompt is what Claude Code passes a PermissionRequest hook
// on stdin.
type ClaudePermissionPrompt struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path,omitempty"`
	Cwd            string          `json:"cwd,omitempty"`
	PermissionMode string          `json:"permission_mode,omitempty"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`
	Suggestions    json.RawMessage `json:"permission_suggestions,omitempty"`
	// PID is the Claude process that ran the hook, which must be the
	// session's own.
	PID int `json:"-"`
}

// ClaudePromptDecision is the decision a PermissionRequest hook returns
// under hookSpecificOutput.decision.
type ClaudePromptDecision struct {
	Behavior           string          `json:"behavior"`
	UpdatedInput       map[string]any  `json:"updatedInput,omitempty"`
	UpdatedPermissions json.RawMessage `json:"updatedPermissions,omitempty"`
	Message            string          `json:"message,omitempty"`
}

// ClaudePromptChange reports that a session gained a held prompt or lost
// one. Settled is set when the prompt was answered, through Agenthail or in
// the session, and unset when Agenthail stopped holding a prompt that may
// still be open, such as when the hook gave up.
type ClaudePromptChange struct {
	SessionID string
	RequestID string
	Open      bool
	Settled   bool
}

// ErrPromptNotHeld means the prompt belongs to a session Agenthail does not
// answer through the hook: one a host such as Agenthail's SDK host answers
// through canUseTool, a background job, or a process Agenthail cannot find.
var ErrPromptNotHeld = errors.New("Agenthail does not hold this session's prompts")

const claudePromptDeclined = "The person declined this request."

// claudePromptRecordWait bounds how long a prompt waits for the session
// record to show it, so the prompt takes the record's name for the wait.
const claudePromptRecordWait = 2 * time.Second

// claudePromptSettleEvery is how often a held prompt checks whether the
// session settled it without Agenthail.
var claudePromptSettleEvery = time.Second

// claudePrompts holds the permission prompts that terminal Claude sessions
// pass to Agenthail through its PermissionRequest hook. Each is held until a
// person answers it through Agenthail, or the session settles it without
// Agenthail: the terminal, cmux or Remote Control answered first, or the
// process ended.
type claudePrompts struct {
	mu   sync.Mutex
	open map[string][]*claudePrompt
	// answered is, per session, when the record showed the last prompt
	// Agenthail answered. The record goes on showing it until Claude acts on
	// the answer, and that is no longer a wait.
	answered map[string]int64
	sequence uint64
	changed  func(ClaudePromptChange)
}

type claudePrompt struct {
	request     surface.Request
	input       map[string]any
	suggestions json.RawMessage
	// shownMS is the record's statusUpdatedAt from when it showed this
	// prompt, or zero before it did.
	shownMS   int64
	createdMS int64
	decided   chan ClaudePromptDecision
	answered  bool
}

func newClaudePrompts() *claudePrompts {
	return &claudePrompts{open: map[string][]*claudePrompt{}, answered: map[string]int64{}}
}

// OnPromptChange registers what runs after a session gains or loses a held
// prompt, so the waiting state and its notifications follow.
func (c *Claude) OnPromptChange(changed func(ClaudePromptChange)) {
	c.prompts.mu.Lock()
	c.prompts.changed = changed
	c.prompts.mu.Unlock()
}

// HoldPrompt holds one prompt of a terminal Claude session until a person
// answers it through Agenthail, which returns the decision, or the session
// settles it without Agenthail, which returns no decision.
func (c *Claude) HoldPrompt(ctx context.Context, prompt ClaudePermissionPrompt) (*ClaudePromptDecision, error) {
	if prompt.SessionID == "" || prompt.ToolName == "" {
		return nil, fmt.Errorf("a prompt names its session and tool")
	}
	pid, ok := c.terminalProcess(prompt.SessionID)
	if !ok || prompt.PID != pid {
		return nil, ErrPromptNotHeld
	}
	var input map[string]any
	if len(prompt.ToolInput) > 0 && json.Unmarshal(prompt.ToolInput, &input) != nil {
		return nil, fmt.Errorf("the prompt's tool_input is not a JSON object")
	}
	if input == nil {
		input = map[string]any{}
	}
	session := surface.Session{ID: prompt.SessionID, PID: pid}
	created := time.Now().UTC()
	held := &claudePrompt{input: input, suggestions: prompt.Suggestions, createdMS: created.UnixMilli(), decided: make(chan ClaudePromptDecision, 1)}
	kind := surface.WaitingApproval
	if prompt.ToolName == "AskUserQuestion" {
		kind = surface.WaitingQuestion
	}
	id := fmt.Sprintf("%s:%s:held:%d", prompt.SessionID, kind, c.prompts.next())
	// The record shows the prompt once Claude does; when it shows it says
	// when the prompt was first asked, and later changes tell this prompt
	// was settled.
	if waiting := c.awaitRecordWaiting(ctx, session); waiting != nil && waiting.Kind == kind && !c.prompts.shown(prompt.SessionID, waiting.Since.UnixMilli()) {
		created, held.shownMS = waiting.Since, waiting.Since.UnixMilli()
	}
	held.request = claudePromptRequest(id, kind, prompt, input, created)
	c.prompts.add(prompt.SessionID, held)
	ticker := time.NewTicker(claudePromptSettleEvery)
	defer ticker.Stop()
	for {
		select {
		case decision := <-held.decided:
			c.prompts.remove(prompt.SessionID, held, true)
			return &decision, nil
		case <-ctx.Done():
			c.prompts.remove(prompt.SessionID, held, false)
			return nil, ctx.Err()
		case <-ticker.C:
			if c.promptSettled(session, held) {
				c.prompts.remove(prompt.SessionID, held, true)
				return nil, nil
			}
		}
	}
}

// terminalProcess finds the live process of a session a person runs in a
// terminal. Hosts that answer through canUseTool, such as Agenthail's SDK
// host, and background jobs are not terminal sessions.
func (c *Claude) terminalProcess(sessionID string) (int, bool) {
	_, records, err := c.records()
	if err != nil {
		return 0, false
	}
	for _, record := range records {
		fields := record.fields
		if str(fields, "sessionId") != sessionID || str(fields, "kind") != "interactive" || str(fields, "entrypoint") != "cli" {
			continue
		}
		if pid, ok := fields["pid"].(float64); ok && pid > 0 && claudeRecordLive(fields) {
			return int(pid), true
		}
	}
	return 0, false
}

func (c *Claude) awaitRecordWaiting(ctx context.Context, session surface.Session) *surface.Waiting {
	deadline := time.NewTimer(claudePromptRecordWait)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if waiting := c.recordWaiting(&session); waiting != nil {
			return waiting
		}
		select {
		case <-ctx.Done():
			return nil
		case <-deadline.C:
			return nil
		case <-tick.C:
		}
	}
}

// promptSettled reports whether the session moved past a held prompt without
// Agenthail: its process ended, it waits on a different prompt, or it stopped
// waiting after showing this one.
func (c *Claude) promptSettled(session surface.Session, held *claudePrompt) bool {
	record, live := c.liveRecord(session)
	if !live {
		return true
	}
	since, _ := record["statusUpdatedAt"].(float64)
	if str(record, "status") == "waiting" {
		c.prompts.mu.Lock()
		defer c.prompts.mu.Unlock()
		if held.shownMS == 0 && int64(since) >= held.createdMS-int64(claudePromptRecordWait/time.Millisecond) {
			held.shownMS = int64(since)
		}
		return held.shownMS != 0 && int64(since) != held.shownMS
	}
	c.prompts.mu.Lock()
	shown := held.shownMS
	c.prompts.mu.Unlock()
	return shown != 0 || int64(since) > held.createdMS
}

func claudePromptRequest(id string, kind surface.WaitingKind, prompt ClaudePermissionPrompt, input map[string]any, created time.Time) surface.Request {
	request := surface.Request{ID: id, Kind: kind, Tool: prompt.ToolName, CreatedAt: created.UTC()}
	if kind == surface.WaitingQuestion {
		var asked struct {
			Questions []surface.Question `json:"questions"`
		}
		_ = json.Unmarshal(prompt.ToolInput, &asked)
		for _, question := range asked.Questions {
			if question.Options == nil {
				question.Options = []surface.QuestionOption{}
			}
			request.Questions = append(request.Questions, question)
		}
		return request
	}
	var suggestions []json.RawMessage
	request.AlwaysAllow = json.Unmarshal(prompt.Suggestions, &suggestions) == nil && len(suggestions) > 0
	if encoded, err := json.Marshal(input); err == nil {
		request.Input = encoded
	}
	return request
}

func (p *claudePrompts) add(sessionID string, held *claudePrompt) {
	p.mu.Lock()
	p.open[sessionID] = append(p.open[sessionID], held)
	changed := p.changed
	p.mu.Unlock()
	if changed != nil {
		changed(ClaudePromptChange{SessionID: sessionID, RequestID: held.request.ID, Open: true})
	}
}

func (p *claudePrompts) remove(sessionID string, held *claudePrompt, settled bool) {
	p.mu.Lock()
	if held.answered && held.shownMS != 0 {
		p.answered[sessionID] = held.shownMS
	}
	p.open[sessionID] = slices.DeleteFunc(p.open[sessionID], func(open *claudePrompt) bool { return open == held })
	if len(p.open[sessionID]) == 0 {
		delete(p.open, sessionID)
	}
	changed := p.changed
	p.mu.Unlock()
	if changed != nil {
		changed(ClaudePromptChange{SessionID: sessionID, RequestID: held.request.ID, Settled: settled})
	}
}

func (p *claudePrompts) next() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sequence++
	return p.sequence
}

// shown reports whether a prompt Agenthail holds or answered is the one the
// record shows since sinceMS.
func (p *claudePrompts) shown(sessionID string, sinceMS int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.answered[sessionID] == sinceMS {
		return true
	}
	for _, held := range p.open[sessionID] {
		if held.shownMS == sinceMS {
			return true
		}
	}
	return false
}

// answeredWait drops a record's wait that is a prompt Agenthail already
// answered, and forgets that answer once the record moves on.
func (p *claudePrompts) answeredWait(sessionID string, waiting *surface.Waiting) *surface.Waiting {
	p.mu.Lock()
	defer p.mu.Unlock()
	answered, found := p.answered[sessionID]
	if !found {
		return waiting
	}
	if waiting != nil && waiting.Since.UnixMilli() == answered {
		return nil
	}
	delete(p.answered, sessionID)
	return waiting
}

func (p *claudePrompts) any(sessionID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.open[sessionID]) > 0
}

func (p *claudePrompts) waiting(sessionID string) *surface.Waiting {
	p.mu.Lock()
	defer p.mu.Unlock()
	open := p.open[sessionID]
	if len(open) == 0 {
		return nil
	}
	oldest := open[0].request
	return &surface.Waiting{Kind: oldest.Kind, ID: oldest.ID, Since: oldest.CreatedAt}
}

func (p *claudePrompts) requests(sessionID string) []surface.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	requests := make([]surface.Request, 0, len(p.open[sessionID]))
	for _, held := range p.open[sessionID] {
		requests = append(requests, held.request)
	}
	return requests
}

// answer turns a person's decision into the hook's decision for one held
// prompt. A prompt already answered, or no longer held, is stale.
func (p *claudePrompts) answer(sessionID, requestID string, decision surface.Decision) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var held *claudePrompt
	for _, open := range p.open[sessionID] {
		if open.request.ID == requestID {
			held = open
		}
	}
	if held == nil || held.answered {
		return "", surface.ErrRequestStale
	}
	outcome, result, err := decidePrompt(held, decision)
	if err != nil {
		return "", err
	}
	held.answered = true
	held.decided <- result
	return outcome, nil
}

// decidePrompt is the Claude host's decide for a hook: a question is
// answered only with an answer to each of its questions, keyed by the
// question's text, and always allowing applies the rules Claude suggested.
func decidePrompt(held *claudePrompt, decision surface.Decision) (string, ClaudePromptDecision, error) {
	switch decision.Behavior {
	case "deny":
		message := strings.TrimSpace(decision.Message)
		if message == "" {
			message = claudePromptDeclined
		}
		return "denied", ClaudePromptDecision{Behavior: "deny", Message: message}, nil
	case "allow":
	default:
		return "", ClaudePromptDecision{}, fmt.Errorf("decision.behavior must be allow or deny")
	}
	if held.request.Kind == surface.WaitingQuestion {
		if decision.Answers == nil {
			return "", ClaudePromptDecision{}, fmt.Errorf("a question is answered with answers")
		}
		for asked := range decision.Answers {
			if !slices.ContainsFunc(held.request.Questions, func(question surface.Question) bool { return question.Question == asked }) {
				return "", ClaudePromptDecision{}, fmt.Errorf("not a question of this request: %s", asked)
			}
		}
		for _, question := range held.request.Questions {
			if strings.TrimSpace(decision.Answers[question.Question]) == "" {
				return "", ClaudePromptDecision{}, fmt.Errorf("no answer to: %s", question.Question)
			}
		}
		input := make(map[string]any, len(held.input)+1)
		for key, value := range held.input {
			input[key] = value
		}
		input["answers"] = decision.Answers
		return "answered", ClaudePromptDecision{Behavior: "allow", UpdatedInput: input}, nil
	}
	if decision.Answers != nil {
		return "", ClaudePromptDecision{}, fmt.Errorf("only a question takes answers")
	}
	if decision.Always {
		if !held.request.AlwaysAllow {
			return "", ClaudePromptDecision{}, fmt.Errorf("this request offers no rule to always allow")
		}
		return "allowed_always", ClaudePromptDecision{Behavior: "allow", UpdatedInput: held.input, UpdatedPermissions: held.suggestions}, nil
	}
	return "allowed", ClaudePromptDecision{Behavior: "allow", UpdatedInput: held.input}, nil
}
