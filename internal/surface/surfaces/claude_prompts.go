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
	// AskedAt is when the hook read the prompt. A hook posts the same
	// prompt again after a daemon restart, so the daemon receiving it may
	// be much later.
	AskedAt time.Time `json:"-"`
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

// claudePromptDuplicateWindow bounds how long an ended hold is remembered. A
// duplicate hook call joins within claudePromptRecordWait of the first.
const claudePromptDuplicateWindow = 2*claudePromptRecordWait + time.Second

// claudePromptSettleEvery is how often a held prompt checks whether the
// session settled it without Agenthail.
var claudePromptSettleEvery = time.Second

// claudePrompts holds the permission prompts that terminal Claude sessions
// pass to Agenthail through its PermissionRequest hook. Each is held until a
// person answers it through Agenthail, or the session settles it without
// Agenthail: the terminal, Remote Control or another PermissionRequest hook
// (cmux installs one, for example) answered first, or the process ended.
type claudePrompts struct {
	mu   sync.Mutex
	open map[string][]*claudePrompt
	// answered is, per session, when the record showed the last prompt
	// Agenthail answered. The record goes on showing it until Claude acts on
	// the answer, and that is no longer a wait.
	answered map[string]claudeAnswer
	// alive reports whether the process with this pid and identity is still
	// running; an answer is forgotten once the process that asked has exited.
	alive func(pid int, identity string) bool
	// ended keeps, per session, the holds that ended lately, so a duplicate
	// hook call that arrives after its prompt was settled takes that outcome
	// rather than holding the prompt again.
	ended map[string][]*claudePrompt
	// forgetAfter is how long an ended hold is remembered.
	forgetAfter time.Duration
	sequence    uint64
	changed     func(ClaudePromptChange)
}

type claudeAnswer struct {
	shownMS  int64
	pid      int
	identity string
}

type claudePrompt struct {
	request     surface.Request
	input       map[string]any
	suggestions json.RawMessage
	// shownMS is the record's statusUpdatedAt from when it showed this
	// prompt, or zero before it did.
	shownMS   int64
	createdMS int64
	// key names the prompt by its tool and input. Claude runs every matching
	// hook, so the same hook installed in more than one settings source
	// passes the same prompt more than once; those calls share one hold.
	key     string
	holders int
	// pid and identity name the asking Claude process across pid reuse.
	pid      int
	identity string
	// recordMS is the statusUpdatedAt of the wait the record showed when the
	// hook called.
	recordMS int64
	// done closes when the hold ends; decision is set before then when a
	// person answered it through Agenthail.
	done     chan struct{}
	closed   bool
	decision *ClaudePromptDecision
}

func newClaudePrompts() *claudePrompts {
	return &claudePrompts{open: map[string][]*claudePrompt{}, answered: map[string]claudeAnswer{}, alive: sameProcessRunning, ended: map[string][]*claudePrompt{}, forgetAfter: claudePromptDuplicateWindow}
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
	if !prompt.AskedAt.IsZero() && prompt.AskedAt.Before(created) {
		created = prompt.AskedAt.UTC()
	}
	canonical, _ := json.Marshal(input)
	held := &claudePrompt{input: input, suggestions: prompt.Suggestions, createdMS: created.UnixMilli(), key: prompt.ToolName + "\x00" + string(canonical), holders: 1, pid: pid, done: make(chan struct{})}
	held.identity, _ = processIdentity(pid)
	kind := surface.WaitingApproval
	if prompt.ToolName == "AskUserQuestion" {
		kind = surface.WaitingQuestion
	}
	id := fmt.Sprintf("%s:%s:held:%d", prompt.SessionID, kind, c.prompts.next())
	waiting := c.awaitRecordWaiting(ctx, session)
	if waiting == nil {
		// Claude shows a prompt in the record as it opens the dialog; one
		// the record does not show was settled before the hook got here.
		return nil, nil
	}
	if waiting.Since.After(created.Add(claudePromptRecordWait)) {
		// The session waits on a prompt it showed after this one, so this
		// one was settled while no daemon held it.
		return nil, ErrPromptNotHeld
	}
	held.recordMS = waiting.Since.UnixMilli()
	if !c.prompts.shown(prompt.SessionID, held.recordMS) {
		created, held.shownMS = waiting.Since, held.recordMS
	}
	held.request = claudePromptRequest(id, kind, prompt, input, created)
	held = c.prompts.join(prompt.SessionID, held)
	ticker := time.NewTicker(claudePromptSettleEvery)
	defer ticker.Stop()
	for {
		select {
		case <-held.done:
			return held.decision, nil
		case <-ctx.Done():
			c.prompts.leave(prompt.SessionID, held)
			return nil, ctx.Err()
		case <-ticker.C:
			if c.promptSettled(session, held) {
				c.prompts.end(prompt.SessionID, held, nil)
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

// sameProcessRunning reports whether pid is still the process with this
// identity. A process whose identity was never read cannot be told from a
// later one given its pid, so it does not count.
func sameProcessRunning(pid int, identity string) bool {
	if identity == "" || !claudeProcessAlive(pid) {
		return false
	}
	actual, err := processIdentity(pid)
	return err == nil && actual == identity
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

// join holds a new prompt, or returns the open hold of the same prompt that
// an earlier call of a duplicate hook started.
func (p *claudePrompts) join(sessionID string, held *claudePrompt) *claudePrompt {
	p.mu.Lock()
	for _, ended := range p.ended[sessionID] {
		if ended.key == held.key && ended.shownMS != 0 && held.recordMS == ended.shownMS {
			p.mu.Unlock()
			return ended
		}
	}
	for _, open := range p.open[sessionID] {
		if open.key == held.key {
			open.holders++
			p.mu.Unlock()
			return open
		}
	}
	p.open[sessionID] = append(p.open[sessionID], held)
	changed := p.changed
	p.mu.Unlock()
	if changed != nil {
		changed(ClaudePromptChange{SessionID: sessionID, RequestID: held.request.ID, Open: true})
	}
	return held
}

// end closes a hold with a person's decision, or with none when the session
// settled the prompt itself. Every caller holding it returns.
func (p *claudePrompts) end(sessionID string, held *claudePrompt, decision *ClaudePromptDecision) {
	p.mu.Lock()
	ended := p.endLocked(sessionID, held, decision)
	changed := p.changed
	p.mu.Unlock()
	if ended && changed != nil {
		changed(ClaudePromptChange{SessionID: sessionID, RequestID: held.request.ID, Settled: true})
	}
}

func (p *claudePrompts) endLocked(sessionID string, held *claudePrompt, decision *ClaudePromptDecision) bool {
	if held.closed {
		return false
	}
	held.closed = true
	held.decision = decision
	p.ended[sessionID] = append(p.ended[sessionID], held)
	time.AfterFunc(p.forgetAfter, func() { p.forget(sessionID, held) })
	if decision != nil && held.shownMS != 0 {
		p.answered[sessionID] = claudeAnswer{shownMS: held.shownMS, pid: held.pid, identity: held.identity}
	}
	p.open[sessionID] = slices.DeleteFunc(p.open[sessionID], func(open *claudePrompt) bool { return open == held })
	if len(p.open[sessionID]) == 0 {
		delete(p.open, sessionID)
	}
	close(held.done)
	return true
}

func (p *claudePrompts) forget(sessionID string, held *claudePrompt) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ended[sessionID] = slices.DeleteFunc(p.ended[sessionID], func(ended *claudePrompt) bool { return ended == held })
	if len(p.ended[sessionID]) == 0 {
		delete(p.ended, sessionID)
	}
}

// leave drops one caller of a hold whose hook stopped waiting. The prompt
// may still be open in the session, so the last caller to leave ends the
// hold without calling it settled.
func (p *claudePrompts) leave(sessionID string, held *claudePrompt) {
	p.mu.Lock()
	held.holders--
	ended := held.holders == 0 && p.endLocked(sessionID, held, nil)
	changed := p.changed
	p.mu.Unlock()
	if ended && changed != nil {
		changed(ClaudePromptChange{SessionID: sessionID, RequestID: held.request.ID})
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
	if answered, found := p.answered[sessionID]; found && answered.shownMS == sinceMS {
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
	if waiting != nil && waiting.Since.UnixMilli() == answered.shownMS {
		return nil
	}
	delete(p.answered, sessionID)
	return waiting
}

// retain forgets the answers of sessions whose Claude process has exited,
// since no later record will show them moving on.
func (p *claudePrompts) retain() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for sessionID, answered := range p.answered {
		if !p.alive(answered.pid, answered.identity) {
			delete(p.answered, sessionID)
		}
	}
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
	var held *claudePrompt
	for _, open := range p.open[sessionID] {
		if open.request.ID == requestID {
			held = open
		}
	}
	if held == nil {
		p.mu.Unlock()
		return "", surface.ErrRequestStale
	}
	outcome, result, err := decidePrompt(held, decision)
	if err != nil {
		p.mu.Unlock()
		return "", err
	}
	p.endLocked(sessionID, held, &result)
	changed := p.changed
	p.mu.Unlock()
	if changed != nil {
		changed(ClaudePromptChange{SessionID: sessionID, RequestID: requestID, Settled: true})
	}
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
