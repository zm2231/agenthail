package surfaces

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	claudeMovedSuffix     = ".agenthail-moved"
	claudeStagedSuffix    = ".agenthail-move"
	ClaudeMoveRetention   = 7 * 24 * time.Hour
	claudeMoveStopTimeout = 20 * time.Second
	claudeMovePollEvery   = 500 * time.Millisecond
)

// claudeMoveJournal records a move as it runs, so a move that stopped part
// way through continues from its last finished phase. The source copies stay
// renamed with claudeMovedSuffix until Retired plus ClaudeMoveRetention.
type claudeMoveJournal struct {
	Conversation string    `json:"conversation"`
	From         string    `json:"from"`
	To           string    `json:"to"`
	ProjectDir   string    `json:"projectDir"`
	Cwd          string    `json:"cwd"`
	Phase        string    `json:"phase"`
	Pieces       []string  `json:"pieces"`
	Resume       bool      `json:"resume"`
	Started      time.Time `json:"started"`
	Retired      time.Time `json:"retired,omitzero"`
}

const (
	claudeMovePhaseStaged   = "staged"
	claudeMovePhaseSwitched = "switched"
	claudeMovePhaseDone     = "done"
)

func claudeMoveJournalDir(home string) string {
	return filepath.Join(home, ".agenthail", "claude-moves")
}

func claudeMoveJournalPath(home, conversation string) string {
	return filepath.Join(claudeMoveJournalDir(home), conversation+".json")
}

func (c *Claude) readMoveJournal(conversation string) (*claudeMoveJournal, error) {
	data, err := os.ReadFile(claudeMoveJournalPath(c.home, conversation))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var journal claudeMoveJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, fmt.Errorf("read move journal for %s: %w", conversation, err)
	}
	return &journal, nil
}

func (c *Claude) writeMoveJournal(journal *claudeMoveJournal) error {
	if err := os.MkdirAll(claudeMoveJournalDir(c.home), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	path := claudeMoveJournalPath(c.home, journal.Conversation)
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

// MoveSession makes a conversation live in another config root, or adopts a
// terminal conversation into the host when the root stays the same. The id,
// and with it every alias, coordinator and queued delivery, is unchanged.
// Sends wait on the conversation lock for the whole move, so none resumes it
// in the old root halfway.
func (c *Claude) MoveSession(ctx context.Context, sess *surface.Session, request surface.MoveRequest) (*surface.MoveResult, error) {
	if request.To.Host != "" {
		return nil, fmt.Errorf("moving a Claude session to another host is not supported yet")
	}
	conversation := sess.ID
	if conversation == "" || strings.ContainsAny(conversation, `/\`) || strings.HasPrefix(conversation, ".") {
		return nil, surface.DeliveryTerminal(fmt.Errorf("Claude session %q is not a conversation id", sess.ID), surface.DeliveryTargetMissing)
	}
	unlock, err := c.lockConversation(ctx, conversation)
	if err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	defer unlock()

	journal, err := c.readMoveJournal(conversation)
	if err != nil {
		return nil, err
	}
	resume := false
	if journal != nil && journal.Phase != claudeMovePhaseDone {
		if err := c.checkMoveTarget(journal, request); err != nil {
			return nil, err
		}
		if journal.Phase != claudeMovePhaseStaged || !claudePathExists(filepath.Join(journal.From, journal.Pieces[0])) {
			return c.finishMove(ctx, sess, journal, &surface.MoveResult{FromRoot: journal.From, ToRoot: journal.To, Copied: true})
		}
		// Nothing was switched yet, so the source may have been resumed and
		// written to since the copy: the move starts over from its checks and
		// restages, keeping the journal's intent to resume.
		request.To.Account, resume = journal.To, journal.Resume
	}

	from, err := c.conversationRoot(conversation)
	if err != nil {
		return nil, err
	}
	if from == "" {
		return nil, surface.DeliveryTerminal(fmt.Errorf("Claude conversation %s has no transcript in any config root", conversation), surface.DeliveryTargetMissing)
	}
	to := from
	if request.To.Account != "" {
		if to, err = surface.ResolveClaudeAccountRoot(c.home, request.To.Account); err != nil {
			return nil, surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
		}
	}
	accounts, _ := surface.ClaudeAccounts(c.home)
	target, _ := surface.ClaudeAccountForRoot(accounts, to)
	source, _ := surface.ClaudeAccountForRoot(accounts, from)
	if to != from && !target.SignedIn {
		return nil, surface.DeliveryTerminal(fmt.Errorf("Claude config directory %s is not signed in; run CLAUDE_CONFIG_DIR=%s claude auth login", to, to), surface.DeliveryInvalidRequest)
	}
	result := &surface.MoveResult{FromRoot: from, ToRoot: to}
	if to != from && source.AccountID != "" && source.AccountID == target.AccountID {
		result.SameAccount = true
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s and %s are signed into the same account, so the session still spends its usage", from, to))
	}

	holder, err := c.moveHolder(ctx, conversation)
	if err != nil {
		return nil, err
	}
	if to == from && (holder != nil && holder.hosted || request.To.Account != "") {
		session := *sess
		if holder != nil {
			adoptClaudeProcess(&session, &holder.session)
		}
		result.Session, result.Unchanged = &session, true
		return result, nil
	}
	if holder != nil {
		if result.Stopped, err = c.quiesce(ctx, conversation, from, holder, request); err != nil {
			return nil, err
		}
	}

	transcripts := c.transcripts.pass([]string{from})(conversation)
	if len(transcripts) != 1 {
		return nil, fmt.Errorf("Claude conversation %s has %d transcripts in %s", conversation, len(transcripts), from)
	}
	transcript := transcripts[0]
	cwd := sess.Cwd
	if holder != nil && holder.session.Cwd != "" {
		cwd = holder.session.Cwd
	}
	if cwd == "" {
		cwd = claudeTranscriptCwd(transcript)
	}
	if cwd == "" {
		return nil, surface.DeliveryTerminal(fmt.Errorf("Claude conversation %s has no known working directory to resume in", conversation), surface.DeliveryTargetMissing)
	}
	if to == from {
		resumed, err := c.resume(ctx, conversation, cwd)
		if err != nil {
			return nil, err
		}
		result.Session = resumed
		return result, nil
	}

	journal = &claudeMoveJournal{Conversation: conversation, From: from, To: to, ProjectDir: filepath.Base(filepath.Dir(transcript)), Cwd: cwd, Resume: resume || holder != nil, Started: time.Now().UTC()}
	journal.Pieces = claudeConversationPieces(from, journal.ProjectDir, conversation)
	if err := c.stageMove(journal); err != nil {
		c.unstageMove(journal)
		return nil, err
	}
	result.Copied = true
	return c.finishMove(ctx, sess, journal, result)
}

func (c *Claude) checkMoveTarget(journal *claudeMoveJournal, request surface.MoveRequest) error {
	if request.To.Account == "" {
		return nil
	}
	to, err := surface.ResolveClaudeAccountRoot(c.home, request.To.Account)
	if err != nil {
		return surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	}
	if to != journal.To {
		return fmt.Errorf("Claude conversation %s has an unfinished move to %s; finish it first with that account", journal.Conversation, journal.To)
	}
	return nil
}

func claudePathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// claudeConversationPieces are the paths, relative to a config root, that
// hold one conversation: its transcript and the per-conversation state
// Claude keeps beside it.
func claudeConversationPieces(root, projectDir, conversation string) []string {
	pieces := []string{filepath.Join("projects", projectDir, conversation+".jsonl")}
	for _, candidate := range []string{
		filepath.Join("projects", projectDir, conversation),
		filepath.Join("file-history", conversation),
		filepath.Join("session-env", conversation),
		filepath.Join("tasks", conversation),
	} {
		if _, err := os.Lstat(filepath.Join(root, candidate)); err == nil {
			pieces = append(pieces, candidate)
		}
	}
	if todos, err := filepath.Glob(filepath.Join(root, "todos", conversation+"-*.json")); err == nil {
		for _, todo := range todos {
			pieces = append(pieces, filepath.Join("todos", filepath.Base(todo)))
		}
	}
	return pieces
}

// stageMove copies every piece into the target root under a staged name the
// transcript index does not read, and checks the transcript copy byte for
// byte. A piece the source no longer has was already switched.
func (c *Claude) stageMove(journal *claudeMoveJournal) error {
	for _, piece := range journal.Pieces {
		source := filepath.Join(journal.From, piece)
		staged := filepath.Join(journal.To, piece) + claudeStagedSuffix
		if _, err := os.Lstat(filepath.Join(journal.To, piece)); err == nil {
			return fmt.Errorf("Claude config directory %s already holds %s; remove it or move the conversation there", journal.To, piece)
		}
		if err := os.RemoveAll(staged); err != nil {
			return err
		}
		if err := copyClaudePiece(source, staged); err != nil {
			return fmt.Errorf("copy %s to %s: %w", piece, journal.To, err)
		}
	}
	transcript := journal.Pieces[0]
	same, err := sameFileContent(filepath.Join(journal.From, transcript), filepath.Join(journal.To, transcript)+claudeStagedSuffix)
	if err != nil {
		return err
	}
	if !same {
		return fmt.Errorf("the copy of %s in %s does not match the source; the conversation changed during the move", transcript, journal.To)
	}
	journal.Phase = claudeMovePhaseStaged
	return c.writeMoveJournal(journal)
}

func (c *Claude) unstageMove(journal *claudeMoveJournal) {
	for _, piece := range journal.Pieces {
		_ = os.RemoveAll(filepath.Join(journal.To, piece) + claudeStagedSuffix)
	}
	_ = os.Remove(claudeMoveJournalPath(c.home, journal.Conversation))
}

// finishMove switches the staged copy live and resumes it when a process
// held it before the move; an exited conversation stays exited, and its next
// message resumes it in the new root. The source
// transcript is renamed away first, so no listing ever finds the
// conversation in both roots; between the two renames it is in neither, and
// the conversation lock holds every send until it is live again.
func (c *Claude) finishMove(ctx context.Context, sess *surface.Session, journal *claudeMoveJournal, result *surface.MoveResult) (*surface.MoveResult, error) {
	if journal.Phase == claudeMovePhaseStaged {
		for _, piece := range journal.Pieces {
			source := filepath.Join(journal.From, piece)
			if err := os.Rename(source, source+claudeMovedSuffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("retire %s: %w", source, err)
			}
		}
		for _, piece := range journal.Pieces {
			live := filepath.Join(journal.To, piece)
			if err := os.Rename(live+claudeStagedSuffix, live); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("switch %s live: %w", live, err)
			}
		}
		journal.Phase = claudeMovePhaseSwitched
		journal.Retired = time.Now().UTC()
		if err := c.writeMoveJournal(journal); err != nil {
			return nil, err
		}
	}
	result.RetiredSource = filepath.Join(journal.From, journal.Pieces[0]) + claudeMovedSuffix
	if err := surface.CarryClaudeProjectConfig(ctx, c.home, journal.From, journal.To, journal.Cwd, c.mcpAdder); err != nil {
		result.Warnings = append(result.Warnings, "project settings were not carried: "+err.Error())
	}
	moved := *sess
	moved.ID, moved.Surface, moved.Cwd = journal.Conversation, surface.KindClaude, journal.Cwd
	moved.Transcript = filepath.Join(journal.To, journal.Pieces[0])
	moved.PID, moved.Bridge, moved.Transport, moved.Status, moved.WaitingOn = 0, "", "", surface.StatusIdle, nil
	if moved.RuntimeKind == "" {
		moved.RuntimeKind = surface.RuntimeClaudeHost
	}
	if journal.Resume {
		holder, err := c.moveHolder(ctx, journal.Conversation)
		if err != nil {
			return nil, err
		}
		if holder != nil {
			adoptClaudeProcess(&moved, &holder.session)
		} else {
			resumed, err := c.resume(ctx, journal.Conversation, journal.Cwd)
			if err != nil {
				return nil, err
			}
			adoptClaudeProcess(&moved, resumed)
		}
	}
	journal.Phase = claudeMovePhaseDone
	if err := c.writeMoveJournal(journal); err != nil {
		return nil, err
	}
	result.Session = &moved
	return result, nil
}

type claudeMoveHolder struct {
	session    surface.Session
	record     map[string]any
	hosted     bool
	background bool
}

// moveHolder returns the live process that holds a conversation, or nil.
func (c *Claude) moveHolder(ctx context.Context, conversation string) (*claudeMoveHolder, error) {
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
		holder := &claudeMoveHolder{record: record.fields, background: str(record.fields, "kind") == "background"}
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

// moveBusy names the work a stop would cut off: a turn, a question or
// permission prompt, a running subagent, or a background shell.
func moveBusy(holder *claudeMoveHolder) string {
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
func (c *Claude) quiesce(ctx context.Context, conversation, root string, holder *claudeMoveHolder, request surface.MoveRequest) (string, error) {
	deadline := time.Now().Add(request.Wait)
	for {
		reason := moveBusy(holder)
		if reason == "" {
			break
		}
		if !time.Now().Before(deadline) {
			return "", surface.DeliveryTerminal(&surface.MoveBusyError{Session: conversation, Reason: reason}, surface.DeliveryInvalidRequest)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(claudeMovePollEvery):
		}
		next, err := c.moveHolder(ctx, conversation)
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
			return "", surface.DeliveryTerminal(&surface.MoveConsentError{Session: conversation, PID: holder.session.PID}, surface.DeliveryInvalidRequest)
		}
		if !claudeRecordLive(holder.record) {
			break
		}
		if err := syscall.Kill(holder.session.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return "", fmt.Errorf("stop terminal Claude process %d: %w", holder.session.PID, err)
		}
		stopped = fmt.Sprintf("terminal process %d", holder.session.PID)
	}
	stop := time.Now().Add(claudeMoveStopTimeout)
	for {
		c.host.forget()
		next, err := c.moveHolder(ctx, conversation)
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
		case <-time.After(claudeMovePollEvery / 5):
		}
	}
}

// PruneClaudeMoves deletes source copies a finished move retired more than
// retention ago, and the journals of those moves.
func PruneClaudeMoves(home string, retention time.Duration, now time.Time) ([]string, error) {
	entries, err := os.ReadDir(claudeMoveJournalDir(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pruned []string
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(claudeMoveJournalDir(home), entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var journal claudeMoveJournal
		if json.Unmarshal(data, &journal) != nil || journal.Phase != claudeMovePhaseDone || journal.Retired.IsZero() || now.Sub(journal.Retired) < retention {
			continue
		}
		for _, piece := range journal.Pieces {
			retired := filepath.Join(journal.From, piece) + claudeMovedSuffix
			if err := os.RemoveAll(retired); err != nil {
				return pruned, err
			}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return pruned, err
		}
		pruned = append(pruned, journal.Conversation)
	}
	return pruned, nil
}

func copyClaudePiece(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	if !info.IsDir() {
		return copyClaudeFile(source, target, info.Mode().Perm())
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(destination, info.Mode().Perm()|0700)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, destination)
		case info.Mode().IsRegular():
			return copyClaudeFile(path, destination, info.Mode().Perm())
		}
		return nil
	})
}

func copyClaudeFile(source, target string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode|0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func sameFileContent(a, b string) (bool, error) {
	digest := func(path string) ([]byte, error) {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			return nil, err
		}
		return hash.Sum(nil), nil
	}
	first, err := digest(a)
	if err != nil {
		return false, err
	}
	second, err := digest(b)
	if err != nil {
		return false, err
	}
	return string(first) == string(second), nil
}
