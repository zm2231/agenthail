package surfaces

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/atomicfile"
	"github.com/zm2231/agenthail/internal/surface"
)

// A rollout file belongs to the thread its name carries; thread/revert adds
// a rollout id after it, and Codex compresses rollouts untouched for a week.
var codexRolloutFileName = regexp.MustCompile(`^rollout-(\d{4})-(\d{2})-(\d{2})T\d{2}-\d{2}-\d{2}-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})?\.jsonl(\.zst)?$`)

const codexMoveWait = 30 * time.Second

func validCodexMoveThread(id string) bool {
	return looksLikeUUID(id) && strings.ToLower(id) == id && !strings.ContainsAny(id, `/\`)
}

func codexRolloutOf(name, threadID string) bool {
	match := codexRolloutFileName.FindStringSubmatch(name)
	return match != nil && match[4] == threadID
}

// codexRolloutDay is the sessions/ folder Codex files a rollout under.
func codexRolloutDay(name string) string {
	match := codexRolloutFileName.FindStringSubmatch(name)
	return filepath.Join(match[1], match[2], match[3])
}

// codexMoveState is what Codex keeps of a thread outside its rollout files:
// its name and goal, which the target sets again.
type codexMoveState struct {
	Rollout string         `json:"rollout"`
	Name    string         `json:"name,omitempty"`
	Goal    *codexMoveGoal `json:"goal,omitempty"`
}

type codexMoveGoal struct {
	Objective   string `json:"objective"`
	Status      string `json:"status"`
	TokenBudget *int64 `json:"tokenBudget,omitempty"`
}

// codexMoveRecord names the rollout files one move took out of sessions/ or
// placed there, so every later step of that move acts on the same files.
type codexMoveRecord struct {
	Files []string `json:"files"`
}

func codexMoveDir(threadID, moveID string) string {
	return filepath.Join(codexHome(), claudeMovedDir, threadID, moveID)
}

func readCodexMoveRecord(threadID, moveID, kind string) (codexMoveRecord, bool, error) {
	var record codexMoveRecord
	data, err := os.ReadFile(filepath.Join(codexMoveDir(threadID, moveID), kind+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return record, false, nil
	}
	if err != nil {
		return record, false, err
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return record, false, fmt.Errorf("Codex move %s: unreadable %s record: %w", moveID, kind, err)
	}
	for _, name := range record.Files {
		if !codexRolloutOf(name, threadID) {
			return record, false, fmt.Errorf("Codex move %s: %s record names %q", moveID, kind, name)
		}
	}
	return record, true, nil
}

func writeCodexMoveRecord(threadID, moveID, kind string, record codexMoveRecord) error {
	dir := codexMoveDir(threadID, moveID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	encoded, _ := json.Marshal(record)
	return atomicfile.Write(filepath.Join(dir, kind+".json"), encoded, 0600)
}

// codexThreadRollouts lists the regular rollout files of a thread in
// sessions/<day>/ (nested) or in a flat folder such as archived_sessions/.
func codexThreadRollouts(dir, threadID string, nested bool) ([]string, error) {
	pattern := filepath.Join(dir, "rollout-*-"+threadID+"*")
	if nested {
		pattern = filepath.Join(dir, "*", "*", "*", "rollout-*-"+threadID+"*")
	}
	found, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, path := range found {
		if !codexRolloutOf(filepath.Base(path), threadID) {
			continue
		}
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files, nil
}

// codexMovesHeld is the rollout each held move declared, so ArchiveMove can
// tell whether archiving changed it.
type codexMovesHeld struct {
	mu       sync.Mutex
	declared map[string]codexDeclaredRollout
}

type codexDeclaredRollout struct {
	name, sha256 string
}

func (h *codexMovesHeld) hold(threadID string, declared codexDeclaredRollout) func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.declared == nil {
		h.declared = map[string]codexDeclaredRollout{}
	}
	h.declared[threadID] = declared
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.declared, threadID)
	}
}

func (h *codexMovesHeld) of(threadID string) (codexDeclaredRollout, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	declared, ok := h.declared[threadID]
	return declared, ok
}

func codexSessionsDir() string { return filepath.Join(codexHome(), "sessions") }

func codexArchivedDir() string { return filepath.Join(codexHome(), "archived_sessions") }

// PrepareMove holds an idle thread for a move: Agenthail's writes to it wait
// until Release, and the thread is refused unless it is idle with nothing
// asked of the person or queued, owns no subagent threads, is no subagent itself and
// continues no other thread's rollout.
func (c *Codex) PrepareMove(ctx context.Context, sess *surface.Session, _ surface.AdoptRequest) (out *surface.MoveOut, err error) {
	if !validCodexMoveThread(sess.ID) {
		return nil, fmt.Errorf("Codex session %q is not a thread id", sess.ID)
	}
	release, err := holdCodexThread(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			release()
		}
	}()
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return nil, err
	}
	defer releaseCodexWriteLock(lock)
	route := *sess
	client, err := c.openRoute(ctx, &route)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	thread, err := c.readThreadObject(ctx, client, sess.ID)
	if err != nil {
		return nil, err
	}
	if err := c.refuseUnmovableThread(ctx, client, &route, thread); err != nil {
		return nil, err
	}
	current := str(thread, "path")
	if current == "" {
		current = codexTranscriptPath(sess)
	}
	owned, err := codexThreadRollouts(codexSessionsDir(), sess.ID, true)
	if err != nil {
		return nil, err
	}
	if current == "" || !containsString(owned, current) {
		return nil, fmt.Errorf("Codex thread %s has no rollout under %s", sess.ID, codexSessionsDir())
	}
	lineage, err := codexRolloutLineage(current, sess.ID)
	if err != nil {
		return nil, surface.MoveRefusal{Reason: fmt.Sprintf("its history continues a rollout it cannot carry (%v); a forked thread cannot move yet", err)}
	}
	for _, segment := range lineage {
		if !containsString(owned, segment.source) {
			return nil, surface.MoveRefusal{Reason: fmt.Sprintf("its history continues %s, outside this thread's rollouts", filepath.Base(segment.source))}
		}
	}
	state := codexMoveState{Rollout: filepath.Base(current), Name: str(thread, "name")}
	if state.Goal, err = c.moveGoal(ctx, client, sess.ID); err != nil {
		return nil, err
	}
	out = &surface.MoveOut{Conversation: sess.ID, Cwd: sess.Cwd, Title: state.Name}
	if out.Cwd == "" {
		out.Cwd = str(thread, "cwd")
	}
	if out.Cwd == "" {
		return nil, fmt.Errorf("Codex thread %s has no known working directory", sess.ID)
	}
	if out.State, err = json.Marshal(state); err != nil {
		return nil, err
	}
	for _, path := range owned {
		if path == current {
			if out.Size, out.SHA256, err = hashFile(path); err != nil {
				return nil, err
			}
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		out.PartsSize += info.Size()
	}
	forget := c.moves.hold(sess.ID, codexDeclaredRollout{name: state.Rollout, sha256: out.SHA256})
	out.Release = func() {
		forget()
		release()
	}
	return out, nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// refuseUnmovableThread refuses a thread a move would cut off from a turn,
// a request or a related thread.
func (c *Codex) refuseUnmovableThread(ctx context.Context, client codexClient, sess *surface.Session, thread map[string]any) error {
	if subagent := codexSubagent(thread); subagent != nil {
		return surface.MoveRefusal{Reason: fmt.Sprintf("it is a subagent of Codex thread %s; move that thread instead", subagent.ParentID)}
	}
	if kind := codexWaitingKind(thread["status"]); kind != "" {
		return surface.MoveRefusal{Reason: fmt.Sprintf("it is waiting on the person (%s); answer it first", kind)}
	}
	active, err := c.activeTurnID(ctx, client, sess.ID)
	if err != nil {
		return fmt.Errorf("inspect active turn: %w", err)
	}
	if active != "" {
		return surface.MoveRefusal{Reason: fmt.Sprintf("turn %s is running; move it when the thread is idle", active)}
	}
	response, err := client.Request(ctx, "thread/list", map[string]any{"ancestorThreadId": sess.ID, "limit": 20}, codexMoveWait)
	if err != nil {
		return fmt.Errorf("list the thread's subagents: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	children, _ := result["data"].([]any)
	var names []string
	for _, child := range children {
		if object, _ := child.(map[string]any); str(object, "id") != "" {
			names = append(names, str(object, "id"))
		}
	}
	if len(names) > 0 {
		return surface.MoveRefusal{Reason: fmt.Sprintf("it has subagent threads (%s); a thread with subagents cannot move yet", strings.Join(names, ", "))}
	}
	// Codex starts a queued message as soon as an idle thread is resumed, so
	// carrying the queue would start turns the person held back here.
	response, err = client.Request(ctx, "thread/queue/list", map[string]any{"threadId": sess.ID, "limit": 1}, codexMoveWait)
	if err != nil {
		return fmt.Errorf("thread/queue/list: %w", err)
	}
	result, _ = response["result"].(map[string]any)
	if queued, _ := result["data"].([]any); len(queued) > 0 {
		return surface.MoveRefusal{Reason: "messages are queued in Codex for it; start or remove them first"}
	}
	return nil
}

func (c *Codex) moveGoal(ctx context.Context, client codexClient, threadID string) (*codexMoveGoal, error) {
	response, err := client.Request(ctx, "thread/goal/get", map[string]any{"threadId": threadID}, codexMoveWait)
	if err != nil {
		return nil, fmt.Errorf("thread/goal/get: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	goal, _ := result["goal"].(map[string]any)
	if goal == nil {
		return nil, nil
	}
	decoded := decodeCodexGoal(goal)
	return &codexMoveGoal{Objective: decoded.Objective, Status: decoded.Status, TokenBudget: decoded.TokenBudget}, nil
}

// ArchiveMove takes the thread out of every Codex runtime here with
// thread/archive, which unloads it and moves its rollouts to
// archived_sessions/. Archiving ends a running turn, so it runs only while
// the thread is idle and Agenthail's writes wait. It may run again after a
// crash: a thread this move already archived stays archived.
func (c *Codex) ArchiveMove(conversation, moveID string) error {
	if !validCodexMoveThread(conversation) || !claudeConversationID(moveID) {
		return fmt.Errorf("Codex session %q or move %q is not a valid id", conversation, moveID)
	}
	declared, held := c.moves.of(conversation)
	if !held {
		return fmt.Errorf("Codex thread %s is not held for a move here", conversation)
	}
	if record, found, err := readCodexMoveRecord(conversation, moveID, "archived"); err != nil {
		return err
	} else if found && codexAllArchived(record) {
		return checkArchivedRollout(conversation, declared)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*codexMoveWait)
	defer cancel()
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return err
	}
	defer releaseCodexWriteLock(lock)
	owned, err := codexThreadRollouts(codexSessionsDir(), conversation, true)
	if err != nil {
		return err
	}
	if len(owned) == 0 {
		return fmt.Errorf("Codex thread %s has no rollout under %s", conversation, codexSessionsDir())
	}
	record := codexMoveRecord{}
	for _, path := range owned {
		record.Files = append(record.Files, filepath.Base(path))
	}
	sess := &surface.Session{ID: conversation, Surface: surface.KindCodex}
	client, err := c.openRoute(ctx, sess)
	if err != nil {
		return err
	}
	defer client.Close()
	active, err := c.activeTurnID(ctx, client, conversation)
	if err != nil {
		return fmt.Errorf("inspect active turn: %w", err)
	}
	if active != "" {
		return fmt.Errorf("turn %s started in Codex thread %s before it was archived", active, conversation)
	}
	if err := writeCodexMoveRecord(conversation, moveID, "archived", record); err != nil {
		return err
	}
	if _, err := client.Request(ctx, "thread/archive", map[string]any{"threadId": conversation}, codexMoveWait); err != nil {
		return fmt.Errorf("thread/archive: %w", err)
	}
	if !codexAllArchived(record) {
		return fmt.Errorf("Codex archived thread %s but not all of %s", conversation, strings.Join(record.Files, ", "))
	}
	return checkArchivedRollout(conversation, declared)
}

// checkArchivedRollout refuses a move whose rollout Codex wrote to while
// archiving it, since the target holds the files to what was declared.
func checkArchivedRollout(conversation string, declared codexDeclaredRollout) error {
	_, digest, err := hashFile(filepath.Join(codexArchivedDir(), declared.name))
	if err != nil {
		return err
	}
	if digest != declared.sha256 {
		return fmt.Errorf("Codex wrote to thread %s's rollout as it archived it, so it no longer matches what the move declared; move it again", conversation)
	}
	return nil
}

func codexAllArchived(record codexMoveRecord) bool {
	for _, name := range record.Files {
		if _, err := os.Lstat(filepath.Join(codexArchivedDir(), name)); err != nil {
			return false
		}
	}
	return len(record.Files) > 0
}

// RestoreMove brings back a thread this move archived with thread/unarchive.
// A move that archived nothing leaves the thread as it is.
func (c *Codex) RestoreMove(conversation, moveID string) error {
	if !validCodexMoveThread(conversation) || !claudeConversationID(moveID) {
		return fmt.Errorf("Codex session %q or move %q is not a valid id", conversation, moveID)
	}
	record, archived, err := readCodexMoveRecord(conversation, moveID, "archived")
	if err != nil || !archived {
		return err
	}
	stillArchived := false
	for _, name := range record.Files {
		if _, err := os.Lstat(filepath.Join(codexArchivedDir(), name)); err == nil {
			stillArchived = true
		}
	}
	if stillArchived {
		ctx, cancel := context.WithTimeout(context.Background(), 2*codexMoveWait)
		defer cancel()
		client, err := c.openManaged(ctx)
		if err != nil {
			return err
		}
		defer client.Close()
		if _, err := client.Request(ctx, "thread/unarchive", map[string]any{"threadId": conversation}, codexMoveWait); err != nil {
			return fmt.Errorf("thread/unarchive: %w", err)
		}
	}
	return os.RemoveAll(codexMoveDir(conversation, moveID))
}

// WriteMoveArchive streams the rollout files this move archived as a flat
// tar of regular files.
func (c *Codex) WriteMoveArchive(w io.Writer, conversation, moveID string) error {
	record, archived, err := readCodexMoveRecord(conversation, moveID, "archived")
	if err != nil {
		return err
	}
	if !archived {
		return fmt.Errorf("Codex thread %s is not archived for move %s", conversation, moveID)
	}
	archive := tar.NewWriter(w)
	for _, name := range record.Files {
		if err := writeMoveFile(archive, filepath.Join(codexArchivedDir(), name), name); err != nil {
			return err
		}
	}
	return archive.Close()
}

func writeMoveFile(archive *tar.Writer, path, name string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if err := archive.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0600, Size: info.Size(), ModTime: info.ModTime()}); err != nil {
		return err
	}
	written, err := io.Copy(archive, file)
	if err == nil && written != info.Size() {
		err = fmt.Errorf("%s changed while it was archived", path)
	}
	return err
}

func decodeCodexMoveState(in surface.MoveIn) (codexMoveState, error) {
	var state codexMoveState
	if err := json.Unmarshal(in.State, &state); err != nil {
		return state, fmt.Errorf("the move's Codex state is unreadable: %w", err)
	}
	if !codexRolloutOf(state.Rollout, in.Conversation) {
		return state, fmt.Errorf("the move names rollout %q, which is not Codex thread %s's", state.Rollout, in.Conversation)
	}
	return state, nil
}

// CheckMoveIn refuses a thread this machine cannot take: one whose copy here,
// in sessions/ or archived_sessions/, the arriving rollout does not extend.
func (c *Codex) CheckMoveIn(in surface.MoveIn) error {
	if !validCodexMoveThread(in.Conversation) {
		return fmt.Errorf("Codex session %q is not a thread id", in.Conversation)
	}
	if !filepath.IsAbs(in.Cwd) {
		return fmt.Errorf("working directory %q is not absolute", in.Cwd)
	}
	if in.Root != "" {
		return fmt.Errorf("a Codex thread cannot be placed in a chosen account")
	}
	state, err := decodeCodexMoveState(in)
	if err != nil {
		return err
	}
	for _, path := range c.rolloutsHere(in.Conversation) {
		if filepath.Base(path) != state.Rollout {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Size() > in.Size {
			return fmt.Errorf("Codex thread %s here is longer than the one arriving; the two copies have diverged", in.Conversation)
		}
	}
	return nil
}

func (c *Codex) rolloutsHere(threadID string) []string {
	placed, _ := codexThreadRollouts(codexSessionsDir(), threadID, true)
	archived, _ := codexThreadRollouts(codexArchivedDir(), threadID, false)
	return append(placed, archived...)
}

// ReceiveMove places an arriving thread's rollouts in sessions/ and resumes
// it in Codex's app-server daemon in the working directory the move mapped,
// then sets its name and goal again. The archive is read as untrusted:
// only this thread's rollout files, as regular files, are accepted, and they
// are placed only once the current rollout matches the declared size and
// digest. A copy this machine archived when the thread last left is
// unarchived first and must begin the arriving one.
func (c *Codex) ReceiveMove(ctx context.Context, in surface.MoveIn, archive io.Reader, admit func() error) (*surface.Session, error) {
	if err := c.CheckMoveIn(in); err != nil {
		return nil, err
	}
	if !claudeConversationID(in.MoveID) {
		return nil, fmt.Errorf("move id %q is not valid", in.MoveID)
	}
	state, _ := decodeCodexMoveState(in)
	release, err := holdCodexThread(ctx, in.Conversation)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := admit(); err != nil {
		return nil, err
	}
	staging := filepath.Join(codexHome(), ".agenthail-move-"+in.MoveID)
	if err := os.RemoveAll(staging); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(staging, 0700); err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	files, err := extractCodexMoveArchive(staging, in, state, archive)
	if err != nil {
		return nil, err
	}
	client, err := c.openManaged(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	loaded, err := c.loadedThreadIDs(ctx, client, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", surface.ErrMoveInPlaced, err)
	}
	if containsString(loaded, in.Conversation) {
		placed := filepath.Join(codexSessionsDir(), codexRolloutDay(state.Rollout), state.Rollout)
		if err := checkPrefix(filepath.Join(staging, state.Rollout), placed); err != nil {
			return nil, fmt.Errorf("%w with a different rollout: Codex thread %s", surface.ErrMoveInRunning, in.Conversation)
		}
		return c.settleMoveIn(ctx, client, in, state)
	}
	if err := c.CheckMoveIn(in); err != nil {
		return nil, err
	}
	if err := c.unarchiveEarlierCopy(ctx, client, in, staging, files); err != nil {
		return nil, err
	}
	if err := c.placeMoveIn(in, staging, files); err != nil {
		return nil, err
	}
	moved := &surface.Session{ID: in.Conversation, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Cwd: in.Cwd}
	_, err = c.codexThreadRequest(ctx, client, codexThreadResume, moved, map[string]any{"threadId": in.Conversation, "cwd": in.Cwd}, 2*codexMoveWait)
	if err != nil {
		if isCodexTimeout(err) || surface.IsDeliveryOutcomeUnknown(err) {
			return nil, fmt.Errorf("%w: thread/resume: %v", surface.ErrMoveInPlaced, err)
		}
		if dropErr := c.dropPlaced(ctx, client, in.Conversation, in.MoveID); dropErr != nil {
			return nil, fmt.Errorf("%w: thread/resume: %v (%v)", surface.ErrMoveInPlaced, err, dropErr)
		}
		return nil, fmt.Errorf("%w: thread/resume: %v", surface.ErrMoveInRefused, err)
	}
	return c.settleMoveIn(ctx, client, in, state)
}

// settleMoveIn sets the resumed thread's name and goal as they were on the
// source. Each step is safe to repeat when the transfer is retried.
func (c *Codex) settleMoveIn(ctx context.Context, client codexClient, in surface.MoveIn, state codexMoveState) (*surface.Session, error) {
	running := func(step string, err error) error {
		return fmt.Errorf("%w: %s: %v", surface.ErrMoveInRunning, step, err)
	}
	if state.Name != "" {
		if _, err := client.Request(ctx, "thread/name/set", map[string]any{"threadId": in.Conversation, "name": state.Name}, codexMoveWait); err != nil {
			return nil, running("thread/name/set", err)
		}
	}
	if state.Goal != nil {
		params := map[string]any{"threadId": in.Conversation, "objective": state.Goal.Objective, "status": state.Goal.Status}
		if state.Goal.TokenBudget != nil {
			params["tokenBudget"] = *state.Goal.TokenBudget
		}
		if _, err := client.Request(ctx, "thread/goal/set", params, codexMoveWait); err != nil {
			return nil, running("thread/goal/set", err)
		}
	}
	thread, err := c.readThreadObject(ctx, client, in.Conversation)
	if err != nil {
		return nil, running("thread/read", err)
	}
	session := codexSession(thread)
	setCodexTransport(&session, codexTransportManaged)
	if session.ID == "" {
		session.ID = in.Conversation
	}
	return &session, nil
}

// unarchiveEarlierCopy brings back a copy of the thread this machine
// archived when it last moved away, so Codex's record of the thread is no
// longer archived; the arriving rollouts then replace it.
func (c *Codex) unarchiveEarlierCopy(ctx context.Context, client codexClient, in surface.MoveIn, staging string, files []string) error {
	archived, err := codexThreadRollouts(codexArchivedDir(), in.Conversation, false)
	if err != nil || len(archived) == 0 {
		return err
	}
	for _, path := range archived {
		name := filepath.Base(path)
		if !containsString(files, name) {
			return fmt.Errorf("Codex thread %s here has archived rollout %s, which the move does not carry", in.Conversation, name)
		}
		if err := checkPrefix(path, filepath.Join(staging, name)); err != nil {
			return fmt.Errorf("Codex thread %s archived here: %w", in.Conversation, err)
		}
	}
	if _, err := client.Request(ctx, "thread/unarchive", map[string]any{"threadId": in.Conversation}, codexMoveWait); err != nil {
		return fmt.Errorf("thread/unarchive the copy here: %w", err)
	}
	return nil
}

// placeMoveIn renames the staged rollouts into sessions/<day>/, keeping any
// shorter copy of one under the move's folder, after recording what it
// places so an abort sets exactly those files aside.
func (c *Codex) placeMoveIn(in surface.MoveIn, staging string, files []string) error {
	if err := writeCodexMoveRecord(in.Conversation, in.MoveID, "placed", codexMoveRecord{Files: files}); err != nil {
		return err
	}
	for _, name := range files {
		dest := filepath.Join(codexSessionsDir(), codexRolloutDay(name), name)
		if err := checkPrefix(dest, filepath.Join(staging, name)); err != nil {
			return fmt.Errorf("Codex rollout %s: %w", name, err)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		if _, err := os.Lstat(dest); err == nil {
			kept := filepath.Join(codexMoveDir(in.Conversation, in.MoveID), "previous", name)
			if err := os.MkdirAll(filepath.Dir(kept), 0700); err != nil {
				return err
			}
			if err := os.Rename(dest, kept); err != nil {
				return err
			}
		}
		if err := os.Rename(filepath.Join(staging, name), dest); err != nil {
			return err
		}
	}
	return nil
}

// DropMoveIn sets aside the rollouts a move placed in sessions/, into the
// move's folder, and runs commit before Agenthail can resume the thread.
func (c *Codex) DropMoveIn(ctx context.Context, conversation, moveID string, admit, commit func() error) error {
	if !validCodexMoveThread(conversation) || !claudeConversationID(moveID) {
		return fmt.Errorf("Codex session %q or move %q is not a valid id", conversation, moveID)
	}
	release, err := holdCodexThread(ctx, conversation)
	if err != nil {
		return err
	}
	defer release()
	if err := admit(); err != nil {
		return err
	}
	client, err := c.openManaged(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", surface.ErrMoveInPlaced, err)
	}
	defer client.Close()
	if err := c.dropPlaced(ctx, client, conversation, moveID); err != nil {
		return err
	}
	return commit()
}

// dropPlaced sets aside what a move placed unless the thread is loaded here.
func (c *Codex) dropPlaced(ctx context.Context, client codexClient, conversation, moveID string) error {
	loaded, err := c.loadedThreadIDs(ctx, client, 0)
	if err != nil {
		return fmt.Errorf("%w: %v", surface.ErrMoveInPlaced, err)
	}
	if containsString(loaded, conversation) {
		return surface.ErrMoveInRunning
	}
	record, placed, err := readCodexMoveRecord(conversation, moveID, "placed")
	if err != nil {
		return fmt.Errorf("%w: %v", surface.ErrMoveInPlaced, err)
	}
	if !placed {
		return nil
	}
	aside := filepath.Join(codexMoveDir(conversation, moveID), "dropped")
	if err := os.MkdirAll(aside, 0700); err != nil {
		return fmt.Errorf("%w: %v", surface.ErrMoveInPlaced, err)
	}
	for _, name := range record.Files {
		if err := renameIfPresent(filepath.Join(codexSessionsDir(), codexRolloutDay(name), name), filepath.Join(aside, name)); err != nil {
			return fmt.Errorf("%w: %v", surface.ErrMoveInPlaced, err)
		}
	}
	return nil
}

// extractCodexMoveArchive writes the archive's rollout files into staging
// and returns their names. Only this thread's rollouts, flat and regular, are
// accepted; the current one must match the declared size and digest and the
// rest stay within the declared size.
func extractCodexMoveArchive(staging string, in surface.MoveIn, state codexMoveState, archive io.Reader) ([]string, error) {
	reader := tar.NewReader(archive)
	var files []string
	current := false
	parts := int64(0)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read the moved thread: %w", err)
		}
		name := header.Name
		if header.Typeflag != tar.TypeReg || !codexRolloutOf(name, in.Conversation) || containsString(files, name) {
			return nil, fmt.Errorf("moved thread holds %q, which is not one of Codex thread %s's rollout files", header.Name, in.Conversation)
		}
		if name == state.Rollout {
			if header.Size != in.Size {
				return nil, fmt.Errorf("the moved rollout is %d bytes, not the %d the move declared", header.Size, in.Size)
			}
			current = true
		} else if header.Size < 0 || header.Size > in.PartsSize-parts {
			return nil, fmt.Errorf("the moved thread's other rollouts are more than the %d bytes the move declared", in.PartsSize)
		} else {
			parts += header.Size
		}
		file, err := os.OpenFile(filepath.Join(staging, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		sum := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(file, sum), reader)
		closeErr := file.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("read the moved thread: %w", copyErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if name == state.Rollout && (written != in.Size || hex.EncodeToString(sum.Sum(nil)) != in.SHA256) {
			return nil, fmt.Errorf("moved rollout of %s is %d bytes with digest %s, not the %d bytes and digest %s the move declared", in.Conversation, written, hex.EncodeToString(sum.Sum(nil)), in.Size, in.SHA256)
		}
		files = append(files, name)
	}
	if !current {
		return nil, fmt.Errorf("moved thread %s has no rollout %s", in.Conversation, state.Rollout)
	}
	return files, nil
}
