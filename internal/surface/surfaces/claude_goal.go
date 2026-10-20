package surfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"time"

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
// so a later read parses only what Claude appended since.
type claudeGoalCursor struct {
	identity string
	offset   int64
	goal     *surface.GoalState
}

// GoalGet is the goal the session's transcript last recorded. A met goal stays
// complete and an impossible one blocked until Claude records another goal or
// a clear.
func (c *Claude) GoalGet(ctx context.Context, sess *surface.Session) (*surface.GoalState, error) {
	path, err := c.sessionTranscript(sess)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	identity := transcriptFileIdentity(info)
	c.goalMu.Lock()
	defer c.goalMu.Unlock()
	cursor, ok := c.goals[path]
	if !ok || cursor.identity != identity || info.Size() < cursor.offset {
		cursor = claudeGoalCursor{identity: identity}
	}
	goal := cursor.goal
	offset, err := scanAppendedJSONL(ctx, path, cursor.offset, maxClaudeTranscriptRecordBytes, func(line []byte) error {
		if record, ok := parseClaudeGoalRecord(line); ok {
			goal = nextClaudeGoal(goal, record)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if c.goals == nil {
		c.goals = map[string]claudeGoalCursor{}
	}
	c.goals[path] = claudeGoalCursor{identity: identity, offset: offset, goal: goal}
	return cloneGoal(goal), nil
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
