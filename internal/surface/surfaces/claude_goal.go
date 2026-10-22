package surfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/zm2231/agenthail/internal/surface"
)

// Claude Code's /goal is a session Stop hook whose evaluator judges the
// condition after every turn. Each verdict is a transcript attachment of type
// goal_status: a set goal is {met:false, sentinel:true}, a turn judged unmet
// is {met:false}, an achieved goal {met:true}, an impossible one
// {met:false, failed:true}, and a goal cleared by /goal clear or by an
// unrecoverable error {met:true, sentinel:true}. A paused goal writes no
// goal_status record, only a "Goal paused" notice, so it reads as active.
var claudeGoalMarker = []byte(`"goal_status"`)

// Claude 2.1.304 refuses a condition longer than this many UTF-16 units, and
// reads any of the clear words as a request to clear the goal.
const claudeGoalConditionLimit = 4000

var claudeGoalClearWords = map[string]bool{"clear": true, "stop": true, "off": true, "reset": true, "none": true, "cancel": true}

var errClaudeGoalPeer = errors.New("Claude's peer socket hands /goal to the model as a message; reach the session through the Agenthail host or Remote Control to set its goal")

type claudeGoalRecord struct {
	Type       string `json:"type"`
	Timestamp  string `json:"timestamp"`
	Attachment struct {
		Type       string `json:"type"`
		Met        bool   `json:"met"`
		Sentinel   bool   `json:"sentinel"`
		Failed     bool   `json:"failed"`
		Condition  string `json:"condition"`
		Reason     string `json:"reason"`
		DurationMs int64  `json:"durationMs"`
		Tokens     int64  `json:"tokens"`
	} `json:"attachment"`
}

// claudeGoalCursor is the goal a transcript's records up to offset describe,
// so a later read parses only what Claude appended since. version counts the
// goal's changes and streamed is the version a stream last sent.
type claudeGoalCursor struct {
	identity string
	offset   int64
	goal     *surface.GoalState
	version  uint64
	streamed uint64
}

// GoalGet is the goal the session's transcript last recorded. A met goal stays
// complete and an impossible one blocked until Claude records another goal or
// a clear.
func (c *Claude) GoalGet(ctx context.Context, sess *surface.Session) (*surface.GoalState, error) {
	path, err := c.sessionTranscript(sess)
	if err != nil || path == "" {
		return nil, err
	}
	c.goalMu.Lock()
	defer c.goalMu.Unlock()
	cursor, err := c.readGoalLocked(ctx, path)
	if err != nil || cursor == nil {
		return nil, err
	}
	return cloneGoal(cursor.goal), nil
}

// streamGoal is the goal of the transcript at path when it changed after a
// stream last sent it, with that change's version.
func (c *Claude) streamGoal(ctx context.Context, path string) (*surface.GoalState, uint64, bool, error) {
	c.goalMu.Lock()
	defer c.goalMu.Unlock()
	cursor, err := c.readGoalLocked(ctx, path)
	if err != nil || cursor == nil || cursor.version == cursor.streamed {
		return nil, 0, false, err
	}
	cursor.streamed = cursor.version
	return cloneGoal(cursor.goal), cursor.version, true, nil
}

func (c *Claude) readGoalLocked(ctx context.Context, path string) (*claudeGoalCursor, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	identity := transcriptFileIdentity(info)
	if c.goals == nil {
		c.goals = map[string]*claudeGoalCursor{}
	}
	cursor, ok := c.goals[path]
	if !ok {
		cursor = &claudeGoalCursor{}
		c.goals[path] = cursor
	}
	goal, offset := cursor.goal, cursor.offset
	if cursor.identity != identity || info.Size() < offset {
		goal, offset = nil, 0
	}
	offset, err = scanAppendedJSONL(ctx, path, offset, maxClaudeTranscriptRecordBytes, func(line []byte) error {
		if record, ok := parseClaudeGoalRecord(line); ok {
			goal = nextClaudeGoal(goal, record)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !sameGoal(cursor.goal, goal) {
		cursor.version++
	}
	cursor.identity, cursor.offset, cursor.goal = identity, offset, goal
	return cursor, nil
}

func parseClaudeGoalRecord(line []byte) (claudeGoalRecord, bool) {
	var record claudeGoalRecord
	if !bytes.Contains(line, claudeGoalMarker) || json.Unmarshal(line, &record) != nil {
		return record, false
	}
	return record, record.Type == "attachment" && record.Attachment.Type == "goal_status"
}

// nextClaudeGoal applies one goal_status record. Claude writes a set record
// again for a goal still in force, so a set record naming the active goal
// keeps when it started.
func nextClaudeGoal(goal *surface.GoalState, record claudeGoalRecord) *surface.GoalState {
	at, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
	verdict := record.Attachment
	if verdict.Met && verdict.Sentinel {
		return nil
	}
	current := goal != nil && goal.Status == surface.GoalStatusActive && goal.Objective == verdict.Condition
	next := surface.GoalState{Objective: verdict.Condition, Status: surface.GoalStatusActive, CreatedAt: at}
	if current {
		next = *goal
	}
	next.UpdatedAt = at
	if verdict.Sentinel {
		return &next
	}
	next.Reason = verdict.Reason
	switch {
	case verdict.Failed:
		next.Status = surface.GoalStatusBlocked
	case verdict.Met:
		next.Status = surface.GoalStatusComplete
	}
	next.TimeUsedSeconds = int64(at.Sub(next.CreatedAt) / time.Second)
	if verdict.DurationMs > 0 {
		next.TimeUsedSeconds = verdict.DurationMs / 1000
	}
	if verdict.Tokens > 0 {
		next.TokensUsed = verdict.Tokens
	}
	return &next
}

func sameGoal(a, b *surface.GoalState) bool {
	if a == nil || b == nil {
		return a == b
	}
	budgets := a.TokenBudget == b.TokenBudget || a.TokenBudget != nil && b.TokenBudget != nil && *a.TokenBudget == *b.TokenBudget
	left, right := *a, *b
	left.TokenBudget, right.TokenBudget = nil, nil
	return budgets && left == right
}

func cloneGoal(goal *surface.GoalState) *surface.GoalState {
	if goal == nil {
		return nil
	}
	copied := *goal
	if goal.TokenBudget != nil {
		budget := *goal.TokenBudget
		copied.TokenBudget = &budget
	}
	return &copied
}

// GoalSet sets or replaces the session's goal with /goal. Claude starts a
// turn toward a goal as soon as it is set.
func (c *Claude) GoalSet(ctx context.Context, sess *surface.Session, text string) error {
	command, err := c.GoalCommand(sess, text)
	if err != nil {
		return err
	}
	return c.RunGoalCommand(ctx, sess, command)
}

func (c *Claude) GoalClear(ctx context.Context, sess *surface.Session) error {
	command, err := c.GoalCommand(sess, "")
	if err != nil {
		return err
	}
	return c.RunGoalCommand(ctx, sess, command)
}

func (c *Claude) GoalCommand(sess *surface.Session, objective string) (string, error) {
	if sess == nil || !c.canSendHumanTurn(sess) {
		return "", surface.DeliveryTerminal(errClaudeGoalPeer, surface.DeliveryInvalidRequest)
	}
	condition := strings.TrimSpace(objective)
	if condition == "" {
		return "/goal clear", nil
	}
	if units := len(utf16.Encode([]rune(condition))); units > claudeGoalConditionLimit {
		return "", surface.DeliveryTerminal(fmt.Errorf("Claude limits a goal to %d characters; this one has %d", claudeGoalConditionLimit, units), surface.DeliveryInvalidRequest)
	}
	if claudeGoalClearWords[strings.ToLower(condition)] {
		return "", surface.DeliveryTerminal(fmt.Errorf("Claude reads the goal %q as a request to clear the goal", condition), surface.DeliveryInvalidRequest)
	}
	return "/goal " + condition, nil
}

// RunGoalCommand sends command as the session's own turn and waits for the
// output Claude records for it: "Goal set", "Goal cleared" and "No goal set"
// are what Claude asked for, and any other output is its refusal.
func (c *Claude) RunGoalCommand(ctx context.Context, sess *surface.Session, command string) error {
	args, ok := strings.CutPrefix(command, "/goal ")
	if !ok || args == "" {
		return surface.DeliveryTerminal(fmt.Errorf("not a Claude goal command: %q", command), surface.DeliveryInvalidRequest)
	}
	if sess == nil || !c.canSendHumanTurn(sess) {
		return surface.DeliveryTerminal(errClaudeGoalPeer, surface.DeliveryInvalidRequest)
	}
	path, err := c.sessionTranscript(sess)
	if err != nil {
		return err
	}
	var offset int64
	if info, statErr := os.Stat(path); statErr == nil {
		offset = info.Size()
	}
	sent, err := c.SendHumanTurn(ctx, sess, command, nil)
	if err != nil {
		return err
	}
	if sent == nil || !sent.Accepted {
		return surface.DeliveryUnavailable(errors.New("Claude is working; the goal command runs when its turn ends"))
	}
	ticker := time.NewTicker(claudeControlPoll)
	defer ticker.Stop()
	for {
		if path == "" {
			if path, err = c.sessionTranscript(sess); err != nil {
				return surface.DeliveryOutcomeUnknown(err)
			}
		}
		output, found, err := claudeGoalCommandOutput(ctx, path, &offset, args)
		if err != nil {
			return surface.DeliveryOutcomeUnknown(fmt.Errorf("read the outcome of %s: %w", command, err))
		}
		if found {
			if strings.HasPrefix(output, "Goal set:") || strings.HasPrefix(output, "Goal cleared:") || output == "No goal set" {
				return nil
			}
			return surface.DeliveryTerminal(fmt.Errorf("Claude did not run %s: %s", command, output), surface.DeliveryInvalidRequest)
		}
		select {
		case <-ctx.Done():
			return surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude accepted %s but its outcome was not observed: %w", command, ctx.Err()))
		case <-ticker.C:
		}
	}
}

// claudeGoalCommandOutput reads what Claude appended after offset for the
// /goal command with these arguments.
func claudeGoalCommandOutput(ctx context.Context, path string, offset *int64, args string) (string, bool, error) {
	if path == "" {
		return "", false, nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Size() < *offset {
		return "", false, errors.New("the transcript was truncated")
	}
	output, found := "", false
	next, err := scanAppendedJSONL(ctx, path, *offset, maxClaudeTranscriptRecordBytes, func(line []byte) error {
		var record claudeRecord
		if found || json.Unmarshal(line, &record) != nil || record.Type != "system" || record.Subtype != "local_command" || record.CommandRun == nil || record.CommandRun.Command != "goal" || strings.TrimSpace(record.CommandRun.Args) != args {
			return nil
		}
		output, _ = claudeLocalCommandOutput(record.Content)
		found = true
		return nil
	})
	*offset = next
	return output, found, err
}
