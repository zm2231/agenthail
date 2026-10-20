package surfaces

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	claudeAdoptStopTimeout = 20 * time.Second
	claudeAdoptPollEvery   = 500 * time.Millisecond
)

// AdoptSession reopens a conversation in Agenthail's host. The process that
// holds it is stopped first once it is idle: a host session is closed, a
// background job stopped, and a terminal process only with the person's
// consent. A session the host already holds is returned unchanged.
func (c *Claude) AdoptSession(ctx context.Context, sess *surface.Session, request surface.AdoptRequest) (*surface.AdoptResult, error) {
	conversation := sess.ID
	if conversation == "" || strings.ContainsAny(conversation, `/\`) || strings.HasPrefix(conversation, ".") {
		return nil, surface.DeliveryTerminal(fmt.Errorf("Claude session %q is not a conversation id", sess.ID), surface.DeliveryTargetMissing)
	}
	unlock, err := c.lockConversation(ctx, conversation)
	if err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	defer unlock()

	root, err := c.conversationRoot(conversation)
	if err != nil {
		return nil, err
	}
	if root == "" {
		return nil, surface.DeliveryTerminal(fmt.Errorf("Claude conversation %s has no transcript in any config root", conversation), surface.DeliveryTargetMissing)
	}
	holder, err := c.adoptHolder(ctx, conversation)
	if err != nil {
		return nil, err
	}
	result := &surface.AdoptResult{}
	if holder != nil && holder.hosted {
		session := *sess
		adoptClaudeProcess(&session, &holder.session)
		result.Session, result.Unchanged = &session, true
		return result, nil
	}
	if holder != nil {
		if result.Stopped, err = c.quiesce(ctx, conversation, root, holder, request); err != nil {
			return nil, err
		}
	}
	transcripts := c.transcripts.pass([]string{root})(conversation)
	if len(transcripts) != 1 {
		return nil, fmt.Errorf("Claude conversation %s has %d transcripts in %s", conversation, len(transcripts), root)
	}
	cwd := sess.Cwd
	if holder != nil && holder.session.Cwd != "" {
		cwd = holder.session.Cwd
	}
	if cwd == "" {
		cwd = claudeTranscriptCwd(transcripts[0])
	}
	if cwd == "" {
		return nil, surface.DeliveryTerminal(fmt.Errorf("Claude conversation %s has no known working directory to resume in", conversation), surface.DeliveryTargetMissing)
	}
	if result.Session, err = c.resume(ctx, conversation, cwd); err != nil {
		return nil, err
	}
	return result, nil
}

type claudeAdoptHolder struct {
	session    surface.Session
	record     map[string]any
	hosted     bool
	background bool
}

// adoptHolder returns the live process that holds a conversation, or nil.
func (c *Claude) adoptHolder(ctx context.Context, conversation string) (*claudeAdoptHolder, error) {
	roots, records, err := c.records()
	if err != nil {
		return nil, err
	}
	transcripts := c.transcripts.pass(roots)
	for _, record := range records {
		if str(record.fields, "sessionId") != conversation {
			continue
		}
		if pid, _ := record.fields["pid"].(float64); pid <= 0 {
			continue
		}
		holder := &claudeAdoptHolder{record: record.fields, background: str(record.fields, "kind") == "background"}
		if process, ok := c.process(ctx, record, transcripts); ok {
			holder.session = process.session
		} else {
			pid, _ := record.fields["pid"].(float64)
			holder.session = surface.Session{ID: conversation, PID: int(pid), Cwd: str(record.fields, "cwd"), Status: claudePeerStatus(record.fields)}
		}
		holder.hosted = claudeHosted(&holder.session)
		return holder, nil
	}
	return nil, nil
}

// adoptBusy names the work a stop would cut off: a turn, a question or
// permission prompt, a running subagent, or a background shell.
func adoptBusy(holder *claudeAdoptHolder) string {
	switch status := str(holder.record, "status"); {
	case status == "busy":
		return "a turn is running"
	case status == "waiting" || holder.record["waitingFor"] != nil:
		return "it is waiting on a question or permission"
	case status == "shell":
		return "a background task is running"
	}
	if holder.session.Status == surface.StatusBusy {
		return "a turn is running"
	}
	if holder.session.WaitingOn != nil {
		return "it is waiting on a question or permission"
	}
	if rollup := holder.session.Subagents; rollup != nil && rollup.Working > 0 {
		return fmt.Sprintf("%d subagents are working", rollup.Working)
	}
	return ""
}

// quiesce stops the process that holds the conversation once it is idle,
// and waits until no process holds it. A terminal process is only stopped
// with the person's consent.
func (c *Claude) quiesce(ctx context.Context, conversation, root string, holder *claudeAdoptHolder, request surface.AdoptRequest) (string, error) {
	deadline := time.Now().Add(request.Wait)
	for {
		reason := adoptBusy(holder)
		if reason == "" {
			break
		}
		if !time.Now().Before(deadline) {
			return "", surface.DeliveryTerminal(&surface.AdoptBusyError{Session: conversation, Reason: reason}, surface.DeliveryInvalidRequest)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(claudeAdoptPollEvery):
		}
		next, err := c.adoptHolder(ctx, conversation)
		if err != nil {
			return "", err
		}
		if next == nil {
			return "", nil
		}
		holder = next
	}
	var stopped string
	switch {
	case holder.hosted:
		if err := c.host.client.Close(ctx, conversation); err != nil && !claudehost.IsCode(err, "not_open") {
			return "", claudeHostDeliveryError(err)
		}
		stopped = "host"
	case holder.background:
		records, err := c.backgroundSessions(ctx, root)
		if err != nil {
			return "", err
		}
		job := ""
		for _, record := range records {
			if record.Kind == "background" && record.SessionID == conversation {
				job = record.ID
			}
		}
		if job == "" {
			return "", fmt.Errorf("Claude conversation %s runs as a background job Claude does not list", conversation)
		}
		if output, err := c.backgroundCommand(ctx, root, "", "stop", job); err != nil {
			return "", surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude stop %s: %w: %s", job, err, string(output)))
		}
		stopped = "background job " + job
	default:
		if !request.StopTerminal {
			return "", surface.DeliveryTerminal(&surface.AdoptConsentError{Session: conversation, PID: holder.session.PID}, surface.DeliveryInvalidRequest)
		}
		if !claudeRecordLive(holder.record) {
			break
		}
		if err := syscall.Kill(holder.session.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return "", fmt.Errorf("stop terminal Claude process %d: %w", holder.session.PID, err)
		}
		stopped = fmt.Sprintf("terminal process %d", holder.session.PID)
	}
	stop := time.Now().Add(claudeAdoptStopTimeout)
	for {
		c.host.forget()
		next, err := c.adoptHolder(ctx, conversation)
		if err != nil {
			return "", err
		}
		if next == nil {
			return stopped, nil
		}
		if !time.Now().Before(stop) {
			return "", surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude conversation %s was asked to stop (%s) but process %d still holds it", conversation, stopped, next.session.PID))
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(claudeAdoptPollEvery / 5):
		}
	}
}
