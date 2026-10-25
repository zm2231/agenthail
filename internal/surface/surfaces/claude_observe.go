package surfaces

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// Observe reads a session whose process exited as offline, unless no other
// process holds its conversation: then it is at rest, and a message resumes it.
func (c *Claude) Observe(ctx context.Context, sess *surface.Session) (*surface.TurnObservation, error) {
	if sess.PID <= 0 || claudeProcessAlive(sess.PID) {
		observation, err := c.observe(ctx, sess)
		if err == nil {
			c.applyRecordObservation(sess, observation)
		}
		return observation, err
	}
	exited := *sess
	exited.Status = surface.StatusOffline
	observation, err := c.observe(ctx, &exited)
	if err == nil && c.atRest(ctx, sess) {
		observation.Status = surface.StatusIdle
		observation.ActiveTurnID = ""
	}
	return observation, err
}

// applyRecordObservation makes the live process's own session record decide
// busy or idle; the transcript still supplies turn and reply evidence, and
// observe supplies what a waiting record waits on.
func (c *Claude) applyRecordObservation(sess *surface.Session, observation *surface.TurnObservation) {
	record, live := c.liveRecord(*sess)
	if !live {
		// With no pid and no record, nothing runs the turn the transcript
		// leaves open.
		if observation.Activity != nil && sess.PID <= 0 && !claudeHosted(sess) {
			observation.Activity.NoProcess = true
		}
		return
	}
	status := claudePeerStatus(record)
	if status == surface.StatusUnknown {
		return
	}
	observation.Status = status
	if status != surface.StatusBusy {
		observation.ActiveTurnID = ""
		observation.WaitingOn = nil
		observation.Activity = nil
		return
	}
	if changed, ok := record["statusUpdatedAt"].(float64); ok && observation.Activity != nil {
		if at := time.UnixMilli(int64(changed)); at.After(observation.Activity.LastProgressAt) {
			observation.Activity.LastProgressAt = at
		}
	}
}

// claudeTurnActivity reads an open turn's start, its newest record and the
// tool call it started last that has no result yet.
func claudeTurnActivity(turn claudeTurn) *surface.Activity {
	activity := &surface.Activity{BusySince: turn.StartedAt, LastProgressAt: turn.LastAt}
	if activity.LastProgressAt.IsZero() {
		activity.LastProgressAt = turn.StartedAt
	}
	if count := len(turn.Tools); count > 0 {
		call := turn.Tools[count-1]
		activity.Tool = &surface.RunningTool{Name: call.Name, StartedAt: call.StartedAt}
	}
	return activity
}

// observe reads the turn from the transcript and what it waits on from the
// process record, or for a session the host runs, from the host, which holds
// its open requests. Claude writes a pending tool call to the transcript only
// once it resolves, so those are the one place a question, a plan approval or
// a permission prompt shows while it is open.
func (c *Claude) observe(ctx context.Context, sess *surface.Session) (*surface.TurnObservation, error) {
	observation, err := c.observeTurn(ctx, sess)
	if err != nil {
		return nil, err
	}
	waiting := c.prompts.answeredWait(sess.ID, c.recordWaiting(sess))
	if held := c.prompts.waiting(sess.ID); held != nil {
		waiting = held
	}
	if claudeHosted(sess) {
		waiting = c.hostWaiting(ctx, sess)
	}
	if observation.UsageLimit != nil && waiting != nil && !waiting.Answerable {
		waiting = nil
	}
	if waiting != nil && observation.Status != surface.StatusOffline {
		observation.Status = surface.StatusBusy
		observation.WaitingOn = waiting
		observation.Activity = nil
	}
	if observation.Status != surface.StatusOffline {
		observation.BackgroundTasks = c.claudeBackground(ctx, sess)
	}
	return observation, nil
}

// recordWaiting reads what the session's live process reports it is waiting
// on: "status":"waiting" with the waitingFor Claude classes itself. Its
// permission prompts, sandbox, worker and goal requests ask for approval;
// "input needed" (AskUserQuestion, an MCP server's question) is a question;
// "dialog open" is a panel only its terminal can settle. statusUpdatedAt is
// when it started waiting, so it names one question.
func (c *Claude) recordWaiting(sess *surface.Session) *surface.Waiting {
	record, live := c.liveRecord(*sess)
	if !live || str(record, "status") != "waiting" {
		return nil
	}
	kind := claudeWaitingKind(str(record, "waitingFor"))
	since, _ := record["statusUpdatedAt"].(float64)
	waiting := &surface.Waiting{Kind: kind, ID: fmt.Sprintf("%s:%s:%d", sess.ID, kind, int64(since))}
	if since > 0 {
		waiting.Since = time.UnixMilli(int64(since)).UTC()
	}
	return waiting
}

func claudeWaitingKind(waitingFor string) surface.WaitingKind {
	switch waitingFor {
	case "input needed":
		return surface.WaitingQuestion
	case "dialog open":
		return surface.WaitingDialog
	default:
		return surface.WaitingApproval
	}
}

func (c *Claude) observeTurn(ctx context.Context, sess *surface.Session) (*surface.TurnObservation, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	observation := &surface.TurnObservation{Status: claudeStatus(sess.Status)}
	if path == "" || !fileExists(path) {
		if observation.Status != surface.StatusOffline {
			observation.Status = surface.StatusUnknown
		}
		return observation, nil
	}
	state, err := c.observeTranscript(ctx, path, sess)
	if err != nil {
		return nil, err
	}
	observation.Plan = state.tasks.plan
	compactPending := state.compactPending()
	if !state.hasCurrent {
		transcriptCanSetReady := observation.Status != surface.StatusOffline || claudeProcessAlive(sess.PID)
		if compactPending && transcriptCanSetReady {
			observation.Status = surface.StatusBusy
			observation.ActiveTurnID = "compact"
		} else if observation.Status != surface.StatusBusy && observation.Status != surface.StatusOffline {
			observation.Status = surface.StatusUnknown
		}
		return observation, nil
	}
	last := state.current
	transcriptCanSetReady := observation.Status != surface.StatusOffline || claudeProcessAlive(sess.PID)
	if !last.Done && !last.Interrupted && transcriptCanSetReady {
		observation.Status = surface.StatusBusy
		observation.ActiveTurnID = last.UserID
		observation.Activity = claudeTurnActivity(last)
	} else if (last.Done || last.Interrupted) && transcriptCanSetReady && !claudeBridgeActivityAfterTerminal(sess, last) {
		observation.Status = surface.StatusIdle
	}
	if compactPending && transcriptCanSetReady {
		observation.Status = surface.StatusBusy
		observation.ActiveTurnID = "compact"
	}
	if state.hasCompleted && state.completed.MessageID != "" {
		observation.CompletedTurnID = state.completed.MessageID
		observation.InputTurnID = state.completed.UserID
		observation.Reply = &surface.ReplyResult{Text: state.completed.Assistant, UserText: state.completed.User, Done: true, Error: claudeTurnError(state.completed)}
	}
	if last.Done && last.UsageLimited && (last.UsageResetsAt.IsZero() || time.Now().Before(last.UsageResetsAt)) {
		observation.UsageLimit = &surface.UsageLimit{ResetsAt: last.UsageResetsAt}
	}
	observation.UsageAccount = claudeTranscriptRoot(path)
	if last.Done && last.UsageLimited {
		observation.LimitStop = &surface.LimitStop{TurnID: last.UserID, RefusedAt: last.TerminalAt, ResetsAt: last.UsageResetsAt}
	} else if last.Done && !last.Interrupted && last.APIError == "" {
		observation.SettledAt = last.TerminalAt
	}
	return observation, nil
}

func claudeBridgeActivityAfterTerminal(sess *surface.Session, turn claudeTurn) bool {
	if claudeStatus(sess.Status) != surface.StatusBusy || sess.LastActive.IsZero() {
		return false
	}
	if turn.TerminalAt.IsZero() {
		return true
	}
	return sess.LastActive.After(turn.TerminalAt.Add(100 * time.Millisecond))
}
