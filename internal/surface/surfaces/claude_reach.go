package surfaces

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

// claudeConversation is what Claude's own records say about one conversation:
// a process Agenthail can deliver to, whether any process holds it, and its
// background job.
var claudeResumeListTimeout = 10 * time.Second

type claudeConversation struct {
	reachable *surface.Session
	alive     bool
	job       *claudeBackground
}

// reach points sess at a live process of its conversation that can take a
// message, resuming the conversation in the Claude host when no process holds
// it. A conversation held by a process that cannot take messages yet returns
// a held delivery, and a resume whose result is uncertain returns an unknown
// outcome. It reports whether it started a process.
func (c *Claude) reach(ctx context.Context, sess *surface.Session) (bool, error) {
	if c.reachableAsIs(ctx, sess) {
		return false, nil
	}
	conversation := sess.ID
	if conversation == "" || strings.ContainsAny(conversation, `/\`) || strings.HasPrefix(conversation, ".") {
		return false, surface.DeliveryTerminal(fmt.Errorf("Claude session %q is not a conversation id", sess.ID), surface.DeliveryTargetMissing)
	}
	unlock, err := c.lockConversation(ctx, conversation)
	if err != nil {
		return false, surface.DeliveryUnavailable(err)
	}
	defer unlock()
	state, err := c.conversation(ctx, conversation, sessionPin(sess))
	if err != nil {
		return false, deliveryError(err)
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
	resumed, err := c.resume(ctx, conversation, cwd)
	if err != nil {
		return false, err
	}
	adoptClaudeProcess(sess, resumed)
	return true, nil
}

// reachRunning points sess at a running process of its conversation that has
// Remote Control, which slash commands need. It never resumes one: a resumed
// conversation runs in the Claude host, which has no Remote Control.
func (c *Claude) reachRunning(ctx context.Context, sess *surface.Session) error {
	if c.reachableAsIs(ctx, sess) {
		return nil
	}
	state, err := c.conversation(ctx, sess.ID, sessionPin(sess))
	if err != nil {
		return deliveryError(err)
	}
	if state.reachable == nil || state.reachable.Bridge == "" {
		return fmt.Errorf("%w: Claude conversation %s has no running process with Remote Control", surface.ErrUnsupported, sess.ID)
	}
	adoptClaudeProcess(sess, state.reachable)
	return nil
}

// NeedsWake reports whether a send to sess would first have to find or resume
// a process for its conversation.
func (c *Claude) NeedsWake(ctx context.Context, sess *surface.Session) bool {
	return !c.reachableAsIs(ctx, sess)
}

// reachableAsIs reports whether sess is attached to a live process that
// still holds its conversation and offers the transport sess would use. It
// refreshes the Remote Control attachment from that process's record.
func (c *Claude) reachableAsIs(ctx context.Context, sess *surface.Session) bool {
	if sess.HolderPID > 0 {
		sess.PID = sess.HolderPID
	}
	if !claudeProcessAlive(sess.PID) {
		return false
	}
	record, ok := c.peerRecord(sess.PID)
	if !ok || str(record, "sessionId") != sess.ID || sess.HolderStart != "" && recordStart(record) != sess.HolderStart {
		return false
	}
	sess.Bridge = ""
	if !c.localOnly {
		sess.Bridge = str(record, "bridgeSessionId")
	}
	if sess.Transport == "uds" {
		return c.peerSocket(ctx, record) != ""
	}
	return claudeHosted(sess) || sess.Bridge != ""
}

// conversation reads which processes hold a conversation. Session records
// show processes that have initialized; the background catalog also shows a
// job that is still starting and has not written its record.
func (c *Claude) conversation(ctx context.Context, id string, pin claudePin) (claudeConversation, error) {
	var state claudeConversation
	process, alive, err := c.conversationProcess(ctx, id, pin)
	if err != nil {
		state.alive = alive
		return state, err
	}
	state.reachable, state.alive = process, alive
	if alive {
		return state, nil
	}
	root, err := c.conversationRoot(id)
	if err != nil {
		return state, err
	}
	jobs, err := c.backgroundSessions(ctx, root)
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

// conversationProcess returns the reachable process that holds a
// conversation, and whether any live process holds it. pin, when set, names
// the holder; a conversation several processes hold is refused without one.
func (c *Claude) conversationProcess(ctx context.Context, id string, pin claudePin) (*surface.Session, bool, error) {
	roots, records, err := c.records()
	if err != nil {
		return nil, false, err
	}
	holders := holderRecords(records, id)
	shared := len(holders) > 1
	if pin.pid > 0 {
		var pinned []map[string]any
		for _, record := range holders {
			if pin.matches(record) {
				pinned = append(pinned, record)
			}
		}
		if len(pinned) == 0 {
			return nil, false, pin.missing(id)
		}
		holders = pinned
	} else if len(holders) > 1 {
		return nil, true, refuseSharedConversation(id, holders)
	}
	if len(holders) == 0 {
		return nil, false, nil
	}
	process, ok := c.process(ctx, claudeSessionRecord{root: holderRoot(records, holders[0]), fields: holders[0]}, c.transcripts.pass(roots))
	if !ok {
		return nil, true, nil
	}
	if shared {
		process.session.HolderPID, process.session.HolderStart = pin.pid, recordStart(holders[0])
	}
	c.enrich(ctx, &process)
	return &process.session, true, nil
}

// deliveryError keeps a delivery classification the lookup already made and
// marks any other lookup failure as a delivery that did not start.
func deliveryError(err error) error {
	if surface.IsDeliveryUnavailable(err) || surface.IsDeliveryTerminal(err) || surface.IsDeliveryOutcomeUnknown(err) {
		return err
	}
	return surface.DeliveryUnavailable(err)
}

// resume opens the conversation in the Claude host, under the config root
// that holds it, and waits until its process is listed. A process from an
// earlier host that still runs holds the conversation, and a Claude that
// fails to start has refused it.
func (c *Claude) resume(ctx context.Context, conversation, cwd string) (*surface.Session, error) {
	root, err := c.conversationRoot(conversation)
	if err != nil {
		return nil, err
	}
	// Claude restores a resumed conversation's title but names the resumed
	// process anew, so the title is passed back as its name.
	opened, err := c.host.client.Open(ctx, claudehost.OpenRequest{Resume: conversation, Cwd: cwd, ConfigDir: surface.ClaudeLaunchConfigDir(c.home, root), Name: c.conversationTitle(ctx, conversation, root)})
	if err != nil {
		return nil, claudeHostResumeError(conversation, err)
	}
	deadline := time.NewTimer(claudeResumeListTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		c.host.forget()
		process, _, err := c.conversationProcess(ctx, conversation, claudePin{pid: opened.PID})
		if err == nil && process != nil && process.PID == opened.PID && claudeHosted(process) {
			return process, nil
		}
		select {
		case <-ctx.Done():
			return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude conversation %s opened in the host as process %d but was not listed: %w", conversation, opened.PID, ctx.Err()))
		case <-deadline.C:
			return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude conversation %s opened in the host as process %d but was not listed", conversation, opened.PID))
		case <-ticker.C:
		}
	}
}

// claudeHostResumeError says why the host did not open a conversation. A
// Claude that exited with an error before it started refused it, which only a
// person can fix; a Claude that could not be started or did not start in time
// is unavailable and retried.
func claudeHostResumeError(conversation string, err error) error {
	switch {
	case claudehost.IsCode(err, "refused"):
		return surface.DeliveryRefused(fmt.Errorf("Claude refused to resume %s: %s", conversation, err))
	case claudehost.IsCode(err, "open_failed"):
		return surface.DeliveryUnavailable(fmt.Errorf("Claude did not resume %s: %s", conversation, err))
	}
	return claudeHostDeliveryError(err)
}

// adoptClaudeProcess attaches process to sess, keeping the Remote Control
// transport a session was addressed through while the process still offers it,
// so a send stays correlated with its transcript turn.
func adoptClaudeProcess(sess, process *surface.Session) {
	if sess.Transport != "" || process.Bridge == "" {
		sess.Transport = process.Transport
	}
	sess.RuntimeKind = process.RuntimeKind
	sess.Bridge = process.Bridge
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
	state, err := c.conversation(ctx, sess.ID, claudePin{})
	return err == nil && !state.alive
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

// conversationTitle is the last title in the conversation's transcript under
// root, the name a person gave it when it started or renamed it.
func (c *Claude) conversationTitle(ctx context.Context, conversation, root string) string {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return ""
	}
	for _, path := range c.transcripts.pass(roots)(conversation) {
		if root == "" || claudeTranscriptRoot(path) == root {
			return claudeTranscriptTitle(ctx, path)
		}
	}
	return ""
}

// maxClaudeTitleRecordBytes bounds a title record: a name reaches Claude in
// a host request, which is at most 1 MiB.
const maxClaudeTitleRecordBytes = 1 << 20

// claudeTranscriptTitle reads the last custom-title record of a transcript.
// A record longer than any title is skipped unread rather than stopping the
// scan.
func claudeTranscriptTitle(ctx context.Context, path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64*1024)
	title := ""
	for ctx.Err() == nil {
		var line []byte
		part, readErr := reader.ReadSlice('\n')
		for readErr == bufio.ErrBufferFull && len(line)+len(part) <= maxClaudeTitleRecordBytes {
			line = append(line, part...)
			part, readErr = reader.ReadSlice('\n')
		}
		line = append(line, part...)
		if readErr == bufio.ErrBufferFull {
			for readErr == bufio.ErrBufferFull {
				_, readErr = reader.ReadSlice('\n')
			}
		} else if bytes.Contains(line, []byte(`"custom-title"`)) {
			var record struct {
				Type        string `json:"type"`
				CustomTitle string `json:"customTitle"`
			}
			if json.Unmarshal(line, &record) == nil && record.Type == "custom-title" && strings.TrimSpace(record.CustomTitle) != "" {
				title = record.CustomTitle
			}
		}
		if readErr != nil {
			break
		}
	}
	return title
}
