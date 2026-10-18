package surfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var claudeResumeSettleTimeout = 10 * time.Second

// claudeConversation is what Claude's own records say about one conversation:
// a process Agenthail can deliver to, whether any process holds it, and its
// background job.
type claudeConversation struct {
	reachable *surface.Session
	alive     bool
	job       *claudeBackground
}

// reach points sess at a live process of its conversation that can take a
// message, resuming the conversation in the background when no process holds
// it. A conversation held by a process that cannot take messages yet returns
// a held delivery, and a resume whose result is uncertain returns an unknown
// outcome. It reports whether it started a process.
func (c *Claude) reach(ctx context.Context, sess *surface.Session) (bool, error) {
	if c.reachableAsIs(ctx, sess) {
		return false, nil
	}
	conversation := c.conversationOf(sess)
	if conversation == "" || strings.ContainsAny(conversation, `/\`) || strings.HasPrefix(conversation, ".") {
		return false, surface.DeliveryTerminal(fmt.Errorf("Claude session %s has no process and no known conversation to resume", sess.ID), surface.DeliveryTargetMissing)
	}
	unlock, err := c.lockConversation(ctx, conversation)
	if err != nil {
		return false, surface.DeliveryUnavailable(err)
	}
	defer unlock()
	state, err := c.conversation(ctx, conversation)
	if err != nil {
		return false, surface.DeliveryUnavailable(err)
	}
	if state.reachable != nil {
		adoptClaudeProcess(sess, state.reachable)
		return false, nil
	}
	if state.alive {
		return false, surface.DeliveryUnavailable(fmt.Errorf("%w: a process holds Claude conversation %s without a messaging socket or Remote Control", surface.ErrDeliveryHeld, conversation))
	}
	cwd := sess.Cwd
	if state.job != nil && state.job.Cwd != "" {
		cwd = state.job.Cwd
	}
	if cwd == "" {
		cwd = claudeTranscriptCwd(sess.Transcript)
	}
	if cwd == "" {
		return false, surface.DeliveryTerminal(fmt.Errorf("Claude conversation %s has no known working directory to resume in", conversation), surface.DeliveryTargetMissing)
	}
	resumed, err := c.resume(ctx, conversation, cwd, state.job, claudeBridgeID(sess.ID))
	if err != nil {
		return false, err
	}
	adoptClaudeProcess(sess, resumed)
	return true, nil
}

// NeedsWake reports whether a send to sess would first have to find or resume
// a process for its conversation.
func (c *Claude) NeedsWake(ctx context.Context, sess *surface.Session) bool {
	return !c.reachableAsIs(ctx, sess)
}

// reachableAsIs reports whether sess already names a live process that owns
// it and offers the transport sess would use.
func (c *Claude) reachableAsIs(ctx context.Context, sess *surface.Session) bool {
	if !claudeProcessAlive(sess.PID) {
		return false
	}
	record, ok := c.peerRecord(sess.PID)
	if !ok || (str(record, "sessionId") != sess.ID && str(record, "bridgeSessionId") != sess.ID) {
		return false
	}
	if sess.Transport == "uds" {
		return c.peerSocket(ctx, record) != ""
	}
	return str(record, "bridgeSessionId") != ""
}

// conversationOf returns the id Claude Code keys the conversation by: the
// transcript name, the session id itself, or for a bridge id the background
// job that registered it.
func (c *Claude) conversationOf(sess *surface.Session) string {
	if sess.Transcript != "" {
		return strings.TrimSuffix(filepath.Base(sess.Transcript), ".jsonl")
	}
	if record, ok := c.peerRecord(sess.PID); ok && claudeProcessAlive(sess.PID) && (str(record, "sessionId") == sess.ID || str(record, "bridgeSessionId") == sess.ID) {
		return str(record, "sessionId")
	}
	if !claudeBridgeID(sess.ID) {
		return sess.ID
	}
	bridge := toCse(sess.ID)
	entries, err := os.ReadDir(filepath.Join(surface.ClaudeConfigDir(c.home), "jobs"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(surface.ClaudeConfigDir(c.home), "jobs", entry.Name(), "state.json"))
		if err != nil {
			continue
		}
		var job struct {
			SessionID       string `json:"sessionId"`
			BridgeSessionID string `json:"bridgeSessionId"`
		}
		if json.Unmarshal(raw, &job) == nil && job.BridgeSessionID != "" && toCse(job.BridgeSessionID) == bridge {
			return job.SessionID
		}
	}
	return ""
}

// conversation reads which processes hold a conversation. Session records
// show processes that have initialized; the background catalog also shows a
// job that is still starting and has not written its record.
func (c *Claude) conversation(ctx context.Context, id string) (claudeConversation, error) {
	var state claudeConversation
	process, alive, err := c.conversationProcess(ctx, id)
	if err != nil {
		return state, err
	}
	state.reachable, state.alive = process, alive
	if alive {
		return state, nil
	}
	jobs, err := c.backgroundSessions(ctx)
	if err != nil {
		return state, err
	}
	for _, job := range jobs {
		if job.Kind != "background" || job.SessionID != id {
			continue
		}
		job := job
		state.job = &job
		if claudeProcessAlive(job.PID) || job.State == "starting" {
			state.alive = true
		}
	}
	return state, nil
}

// conversationProcess returns the earliest started reachable process of a
// conversation, and whether any live process holds it.
func (c *Claude) conversationProcess(ctx context.Context, id string) (*surface.Session, bool, error) {
	records, err := c.records()
	if err != nil {
		return nil, false, err
	}
	transcript := c.transcripts.pass()
	alive := false
	var best *claudeProcess
	for _, record := range records {
		if str(record, "sessionId") != id {
			continue
		}
		if pid, _ := record["pid"].(float64); pid <= 0 {
			continue
		}
		alive = true
		if process, ok := c.process(ctx, record, transcript); ok && (best == nil || process.startedAt < best.startedAt) {
			best = &process
		}
	}
	if best == nil {
		return nil, alive, nil
	}
	return &best.session, true, nil
}

// resume continues a conversation in the background without a prompt, so the
// message is sent only once the resumed process is reachable. Claude starts a
// copy under a new id instead when it finds the conversation running; a copy
// is stopped and the outcome is unknown. Claude exiting with an error before
// it printed a backgrounded line started nothing, such as a workspace it does
// not trust, so the resume failed with Claude's message.
func (c *Claude) resume(ctx context.Context, conversation, cwd string, job *claudeBackground, bridged bool) (*surface.Session, error) {
	started := time.Now()
	output, err := c.backgroundCommand(ctx, cwd, "--bg", "--resume", conversation)
	id, idErr := claudeBackgroundID(string(output))
	var exit *exec.ExitError
	if err != nil && idErr != nil && ctx.Err() == nil && errors.As(err, &exit) && exit.Exited() {
		return nil, surface.DeliveryUnavailable(fmt.Errorf("Claude refused to resume %s: %s", conversation, strings.TrimSpace(claudeANSI.ReplaceAllString(string(output), ""))))
	}
	if err != nil {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude resume: %w: %s", err, string(output)))
	}
	if idErr != nil {
		return nil, surface.DeliveryOutcomeUnknown(idErr)
	}
	if (job != nil && id != job.ID) || (job == nil && !strings.HasPrefix(conversation, id)) {
		return nil, surface.DeliveryOutcomeUnknown(c.stopCopy(conversation, id))
	}
	return c.awaitReachable(ctx, conversation, id, started, bridged)
}

func (c *Claude) stopCopy(conversation, id string) error {
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := c.backgroundCommand(stopCtx, "", "stop", id); err != nil {
		return fmt.Errorf("Claude started copy %s instead of resuming %s, and stopping it failed: %w: %s", id, conversation, err, string(output))
	}
	return fmt.Errorf("Claude started copy %s instead of resuming %s; the copy was stopped", id, conversation)
}

// awaitReachable waits for the resumed process to become reachable. Claude
// prints its backgrounded line before the session initializes, and the job
// state is not evidence: a resumed idle job keeps reporting done. A failure
// counts only when this resume wrote it, since Claude records updatedAt to the
// millisecond. A session addressed by its Remote Control ID is reached only
// once the process registers Remote Control again, so its identity does not
// pass through the socket-only ID; the socket is used if it never does.
func (c *Claude) awaitReachable(ctx context.Context, conversation, id string, started time.Time, bridged bool) (*surface.Session, error) {
	started = started.Truncate(time.Millisecond)
	deadline := time.NewTimer(claudeResumeSettleTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var socketOnly *surface.Session
	for {
		process, alive, err := c.conversationProcess(ctx, conversation)
		if err == nil && process != nil {
			if !bridged || claudeBridgeID(process.ID) {
				return process, nil
			}
			socketOnly = process
		}
		if job, ok := c.backgroundJob(id); ok && !alive && !job.UpdatedAt.Before(started) && (job.State == "failed" || job.State == "crashed") {
			detail := job.Detail
			if strings.TrimSpace(detail) == "" {
				detail = "no job detail recorded"
			}
			return nil, surface.DeliveryUnavailable(fmt.Errorf("Claude background job %s is %s after resume: %s", id, job.State, detail))
		}
		select {
		case <-ctx.Done():
			return nil, surface.DeliveryOutcomeUnknown(ctx.Err())
		case <-deadline.C:
			if socketOnly != nil {
				return socketOnly, nil
			}
			return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude background job %s did not become reachable after resume; inspect claude agents", id))
		case <-ticker.C:
		}
	}
}

// adoptClaudeProcess points sess at process, keeping the Remote Control
// transport a session was addressed through while the process still offers it,
// so a send stays correlated with its transcript turn.
func adoptClaudeProcess(sess, process *surface.Session) {
	if sess.Transport != "" || !claudeBridgeID(process.ID) {
		sess.Transport = process.Transport
	}
	sess.ID = process.ID
	sess.PID = process.PID
	sess.Status = process.Status
	sess.Cwd = process.Cwd
	sess.Transcript = process.Transcript
	sess.HasLocal = process.HasLocal
	sess.StartedAt = process.StartedAt
	sess.LastActive = process.LastActive
	if process.Name != "" {
		sess.Name = process.Name
	}
}

// lockConversation serializes resumes of one conversation across Agenthail
// processes, so two senders cannot both find it without a process and start
// two.
func (c *Claude) lockConversation(ctx context.Context, conversation string) (func(), error) {
	dir := filepath.Join(c.home, ".agenthail", "claude-resume")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, conversation+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, fmt.Errorf("wait for Claude conversation %s: %w", conversation, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// atRest reports whether a session whose process exited belongs to a
// conversation no other process holds, so a message would resume it.
func (c *Claude) atRest(ctx context.Context, sess *surface.Session) bool {
	conversation := c.conversationOf(sess)
	if conversation == "" {
		return true
	}
	state, err := c.conversation(ctx, conversation)
	return err == nil && !state.alive
}

func claudeBridgeID(id string) bool {
	return strings.HasPrefix(id, "session_") || strings.HasPrefix(id, "cse_")
}

func claudeTranscriptCwd(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), maxClaudeTranscriptRecordBytes)
	for scanner.Scan() {
		var record struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(scanner.Bytes(), &record) == nil && record.Cwd != "" {
			return record.Cwd
		}
	}
	return ""
}
