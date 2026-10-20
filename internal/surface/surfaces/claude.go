package surfaces

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

type Claude struct {
	signins      signin.Resolver
	home         string
	cookieBridge string
	contextMu    sync.Mutex
	contextState map[string]*claudeContextState
	observeMu    sync.Mutex
	observeState map[string]*claudeObservationState
	modelsMu     sync.Mutex
	modelsCache  []surface.ModelOption
	modelsAt     time.Time
	modelsFlight *claudeModelsFlight
	subagents    *claudeSubagentObserver
	transcripts  *claudeTranscriptIndex
	host         *claudeHost
	prompts      *claudePrompts
	request      ClaudeRequest
	localOnly    bool
}

type ClaudeRequest func(context.Context, string, string, map[string]string, string, CookieSource, time.Duration) (int, string, error)

func NewClaude(signins signin.Resolver, home string) *Claude {
	return NewClaudeWithRequest(signins, home, claudeSendRequest)
}

func NewClaudeWithRequest(signins signin.Resolver, home string, request ClaudeRequest) *Claude {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return &Claude{signins: signins, home: home, cookieBridge: configuredCookieBridge(), subagents: newClaudeSubagentObserver(), request: request, transcripts: newClaudeTranscriptIndex(), host: newClaudeHost(home), prompts: newClaudePrompts()}
}

func (c *Claude) Name() surface.SurfaceKind { return surface.KindClaude }

func (c *Claude) RuntimeKinds() []surface.RuntimeKind {
	return []surface.RuntimeKind{surface.RuntimeClaudeCLI, surface.RuntimeClaudeHost}
}

func (c *Claude) ObserveClaudeRuns(ctx context.Context) ([]surface.ClaudeRunObservation, error) {
	return ObserveClaudeRuns(ctx, c.home)
}

func (c *Claude) ObserveClaudeSubagents(ctx context.Context, session *surface.Session) ([]surface.ClaudeSubagentLink, error) {
	return c.subagents.observeSession(ctx, session)
}

func (c *Claude) ObserveAllClaudeSubagents(ctx context.Context) ([]surface.ClaudeSubagentLink, error) {
	return ObserveAllClaudeSubagents(ctx, c.home)
}

func (c *Claude) Capabilities() surface.Capabilities {
	return surface.Capabilities{
		Send: true, Stream: true, Reply: true, Goal: false,
		Compact: true, Model: true, Interrupt: true, Steer: true,
		Attach: true, Commands: true,
	}
}

// SessionCapabilities narrows Capabilities for a session the host runs, which
// takes steer, interrupt and model changes but no Remote Control commands, and
// for any other session Control cannot reach through Remote Control: a CLI
// session reached only over its native peer socket, or any session on an
// install without Remote Control. Hosted and socket sessions stream only from
// a local transcript.
func (c *Claude) SessionCapabilities(sess *surface.Session) surface.Capabilities {
	capabilities := c.Capabilities()
	if claudeHosted(sess) {
		capabilities.Stream = sess.HasLocal && sess.Transcript != ""
		capabilities.Compact = false
		capabilities.Commands = false
		capabilities.Respond = true
		capabilities.PermissionMode = true
		return capabilities
	}
	if sess == nil {
		return capabilities
	}
	if sess.Transport == "uds" {
		capabilities.Stream = sess.HasLocal && sess.Transcript != ""
	}
	capabilities.Respond = c.prompts.any(sess.ID)
	if c.localOnly || nativeClaudeOnly(sess) {
		capabilities.Steer = false
		capabilities.Compact = false
		capabilities.Model = false
		capabilities.Interrupt = false
		capabilities.Commands = false
	}
	return capabilities
}

// DisableRemoteControl limits the surface to local Claude Code sessions on an
// install that ships no sidecar, so health no longer probes claude.ai.
func (c *Claude) DisableRemoteControl() {
	c.localOnly = true
}

func (c *Claude) Health(ctx context.Context) error {
	sessions, err := c.List(ctx)
	native, bridged := false, false
	for _, session := range sessions {
		native = native || session.Transport == "uds"
		bridged = bridged || session.Bridge != ""
	}
	if c.localOnly {
		if native {
			return nil
		}
		return err
	}
	cookies, signinErr := c.cookieSource("")
	if signin.IsOff(signinErr) {
		if bridged {
			return signinErr
		}
		return err
	}
	if native {
		return nil
	}
	if signinErr != nil {
		return signinErr
	}
	if _, err := sidecarPath(); err != nil {
		return err
	}
	if c.cookieBridge == "" || !fileExists(c.cookieBridge) {
		return fmt.Errorf("Claude cookie bridge not found (set AGENTHAIL_COOKIE_BRIDGE or install cookie.mjs alongside agenthail)")
	}
	status, body, err := sidecarRequestWithCookies(ctx, "GET", "https://claude.ai/api/organizations", c.headerMap("", ""), "", cookies, 15*time.Second)
	if err != nil {
		return fmt.Errorf("Claude authentication probe: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("Claude authentication probe returned HTTP %d: %s", status, diagnosticExcerpt(body))
	}
	return nil
}

func signinTerminalKind(err error) surface.DeliveryTerminalKind {
	var problem signin.ProblemError
	switch {
	case signin.IsOff(err):
		return surface.DeliverySigninOff
	case errors.As(err, &problem):
		return surface.DeliverySigninUnreadable
	}
	return surface.DeliveryAuthenticationNeeded
}

// cookieSource resolves the browser profile for one claude.ai request. It runs
// before anything is posted, so an unset profile never becomes an uncertain
// delivery.
func (c *Claude) cookieSource(sessionID string) (CookieSource, error) {
	source, err := signin.Resolve(c.signins, signin.Target{Consumer: signin.Claude, SessionID: sessionID})
	if err != nil {
		return CookieSource{}, err
	}
	return CookieSource{Bridge: c.cookieBridge, URL: "https://claude.ai/", Signin: source}, nil
}

func (c *Claude) headerMap(_, _ string) map[string]string {
	return map[string]string{
		"user-agent":                chromeUA,
		"accept":                    "application/json",
		"accept-language":           "en-US,en;q=0.9",
		"origin":                    "https://claude.ai",
		"referer":                   "https://claude.ai/code/",
		"anthropic-version":         "2023-06-01",
		"anthropic-beta":            "ccr-byoc-2025-07-29",
		"anthropic-client-platform": "web_claude_ai",
		"anthropic-client-feature":  "ccr",
		"anthropic-client-version":  "1.0.0",
	}
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]), hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]), hex.EncodeToString(b[10:16]))
}

const alphanum = "abcdefghijklmnopqrstuvwxyz0123456789"

func randSeq(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = alphanum[int(b[i])%len(alphanum)]
	}
	return string(b)
}

func toCse(bridgeID string) string {
	s := bridgeID
	s = strings.TrimPrefix(s, "https://claude.ai/code/")
	s = strings.TrimPrefix(s, "session_")
	s = strings.TrimPrefix(s, "cse_")
	return "cse_" + s
}

func (c *Claude) transcriptPath(s *surface.Session) string {
	return c.resolveTranscript(s.ID)
}

// resolveTranscript returns the transcript Claude Code wrote for a
// conversation, or "" when none exists yet; callers resolve again later.
func (c *Claude) resolveTranscript(conversationID string) string {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return ""
	}
	return onlyTranscript(c.transcripts.pass(roots)(conversationID))
}

func onlyTranscript(paths []string) string {
	if len(paths) != 1 {
		return ""
	}
	return paths[0]
}

// claudeTranscriptIndex maps conversation ids to transcripts. Claude Code
// writes every conversation to <root>/projects/<project>/<id>.jsonl, so the
// index is built from directory listings and never predicts project names.
// Each pass re-lists only project directories whose modification time
// changed, which is when Claude Code adds or removes a transcript.
type claudeTranscriptIndex struct {
	mu   sync.Mutex
	dirs map[string]claudeProjectListing
	byID map[string]map[string]struct{}
}

type claudeProjectListing struct {
	modified time.Time
	ids      []string
}

func newClaudeTranscriptIndex() *claudeTranscriptIndex {
	return &claudeTranscriptIndex{dirs: map[string]claudeProjectListing{}, byID: map[string]map[string]struct{}{}}
}

// pass brings the index up to date across roots once and returns a lookup
// over that state, so a discovery pass lists each projects directory one
// time however many sessions it resolves. The lookup returns every
// transcript of an id; one in more than one project resolves to none.
func (x *claudeTranscriptIndex) pass(roots []string) func(conversationID string) []string {
	x.mu.Lock()
	x.refresh(roots)
	x.mu.Unlock()
	return func(conversationID string) []string {
		if conversationID == "" || strings.ContainsAny(conversationID, `/\`) {
			return nil
		}
		x.mu.Lock()
		defer x.mu.Unlock()
		var paths []string
		for dir := range x.byID[conversationID] {
			paths = append(paths, filepath.Join(dir, conversationID+".jsonl"))
		}
		sort.Strings(paths)
		return paths
	}
}

func (x *claudeTranscriptIndex) refresh(roots []string) {
	present := map[string]bool{}
	for _, root := range roots {
		projects := filepath.Join(root, "projects")
		entries, err := os.ReadDir(projects)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(projects, entry.Name())
			present[dir] = true
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if listing, ok := x.dirs[dir]; ok && listing.modified.Equal(info.ModTime()) {
				continue
			}
			x.forget(dir)
			x.list(dir, info.ModTime())
		}
	}
	for dir := range x.dirs {
		if !present[dir] {
			x.forget(dir)
		}
	}
}

func (x *claudeTranscriptIndex) list(dir string, modified time.Time) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	listing := claudeProjectListing{modified: modified}
	for _, file := range files {
		id, ok := strings.CutSuffix(file.Name(), ".jsonl")
		if !ok || file.IsDir() {
			continue
		}
		listing.ids = append(listing.ids, id)
		if x.byID[id] == nil {
			x.byID[id] = map[string]struct{}{}
		}
		x.byID[id][dir] = struct{}{}
	}
	x.dirs[dir] = listing
}

func (x *claudeTranscriptIndex) forget(dir string) {
	for _, id := range x.dirs[dir].ids {
		delete(x.byID[id], dir)
		if len(x.byID[id]) == 0 {
			delete(x.byID, id)
		}
	}
	delete(x.dirs, dir)
}

func (c *Claude) firstUserMessage(path string) string {
	if path == "" || !fileExists(path) {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for sc.Scan() {
		var e struct {
			Type string `json:"type"`
			Msg  struct {
				Content any `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if e.Type != "user" {
			continue
		}
		text := ""
		switch c := e.Msg.Content.(type) {
		case string:
			text = c
		case []any:
			for _, item := range c {
				if m, ok := item.(map[string]any); ok {
					if t, _ := m["type"].(string); t == "text" {
						if s, _ := m["text"].(string); s != "" {
							text = s
							break
						}
					}
				}
			}
		}
		if strings.HasPrefix(text, "<local-command") || strings.HasPrefix(text, "<command-") || strings.HasPrefix(text, "<system") {
			continue
		}
		if text = strings.TrimSpace(text); text != "" {
			return surface.TruncateString(text, 60)
		}
	}
	return ""
}

// List returns one session per id. A conversation resumed in a second process
// without Remote Control carries the same session id; the process that opened
// it first keeps the id, so the later one never takes over its row.
func (c *Claude) List(ctx context.Context) ([]surface.Session, error) {
	processes, err := c.processes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]surface.Session, 0, len(processes))
	started := make([]float64, 0, len(processes))
	owner := map[string]int{}
	for _, process := range processes {
		if index, seen := owner[process.session.ID]; seen {
			if process.startedAt < started[index] {
				out[index], started[index] = process.session, process.startedAt
			}
			continue
		}
		owner[process.session.ID] = len(out)
		out = append(out, process.session)
		started = append(started, process.startedAt)
	}
	return out, nil
}

type claudeProcess struct {
	session   surface.Session
	startedAt float64
}

// processes returns a session for every live Claude process, including
// processes that share a session id.
func (c *Claude) processes(ctx context.Context) ([]claudeProcess, error) {
	roots, records, err := c.records()
	if err != nil {
		return nil, err
	}
	var out []claudeProcess
	transcripts := c.transcripts.pass(roots)
	c.prompts.retain()
	for _, record := range records {
		if process, ok := c.process(ctx, record, transcripts); ok {
			out = append(out, process)
		}
	}
	return out, nil
}

// claudeSessionRecord is a Claude process's session record and the config
// root it was written under.
type claudeSessionRecord struct {
	root   string
	fields map[string]any
}

// records returns the config roots and the session records of Claude
// processes that are alive in any of them, excluding the records Agenthail
// writes for its own peer workers and relays.
func (c *Claude) records() ([]string, []claudeSessionRecord, error) {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return nil, nil, err
	}
	var out []claudeSessionRecord
	for _, root := range roots {
		sessionsDir := filepath.Join(root, "sessions")
		entries, err := os.ReadDir(sessionsDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, fmt.Errorf("read Claude session directory: %w", err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(sessionsDir, e.Name()))
			if err != nil {
				continue
			}
			var m map[string]any
			if json.Unmarshal(raw, &m) != nil || str(m, "agenthail") != "" {
				continue
			}
			if pid, ok := m["pid"].(float64); ok && pid > 0 && !claudeRecordLive(m) {
				continue
			}
			out = append(out, claudeSessionRecord{root: root, fields: m})
		}
	}
	return roots, out, nil
}

// conversationRoot returns the config root that holds a conversation's
// session records and transcripts, or "" when no root holds it yet. A
// conversation found in two roots is refused rather than guessed.
func (c *Claude) conversationRoot(conversationID string) (string, error) {
	roots, records, err := c.records()
	if err != nil {
		return "", err
	}
	found := map[string]bool{}
	for _, record := range records {
		if str(record.fields, "sessionId") == conversationID {
			found[record.root] = true
		}
	}
	return claudeHoldingRoot(conversationID, roots, found, c.transcripts.pass(roots)(conversationID))
}

func claudeHoldingRoot(conversationID string, roots []string, found map[string]bool, transcripts []string) (string, error) {
	for _, path := range transcripts {
		found[claudeTranscriptRoot(path)] = true
	}
	var holding []string
	for _, root := range roots {
		if found[root] {
			holding = append(holding, root)
		}
	}
	if len(holding) > 1 {
		return "", surface.DeliveryTerminal(fmt.Errorf("Claude conversation %s exists in more than one Claude config directory (%s); remove it from all but one", conversationID, strings.Join(holding, ", ")), surface.DeliveryInvalidRequest)
	}
	if len(holding) == 0 {
		return "", nil
	}
	return holding[0], nil
}

func claudeTranscriptRoot(path string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(path)))
}

// sessionTranscript returns the transcript a read of sess uses, refusing a
// conversation whose transcripts lie in more than one config root.
func (c *Claude) sessionTranscript(sess *surface.Session) (string, error) {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return "", err
	}
	paths := c.transcripts.pass(roots)(sess.ID)
	if _, err := claudeHoldingRoot(sess.ID, roots, map[string]bool{}, paths); err != nil {
		return "", err
	}
	if sess.Transcript != "" {
		return sess.Transcript, nil
	}
	return onlyTranscript(paths), nil
}

// EnsureWritable refuses a conversation that more than one config root
// holds, since a send or control could reach either copy. Every write path of
// the adapter checks it itself as well, whoever calls it.
func (c *Claude) EnsureWritable(_ context.Context, sess *surface.Session) error {
	if sess == nil {
		return nil
	}
	_, err := c.conversationRoot(sess.ID)
	return err
}

// process builds the session for a record Agenthail can deliver to: one with
// a Remote Control bridge or a validated messaging socket. The session is the
// record's conversation; the bridge and socket belong to the process.
func (c *Claude) process(ctx context.Context, record claudeSessionRecord, transcripts func(string) []string) (claudeProcess, bool) {
	m := record.fields
	conversation := str(m, "sessionId")
	bridge := ""
	if !c.localOnly {
		bridge = str(m, "bridgeSessionId")
	}
	socket := c.peerSocket(ctx, m)
	pid, _ := m["pid"].(float64)
	hosted := c.host.runs(ctx, int(pid), conversation)
	if conversation == "" || (bridge == "" && socket == "" && !hosted) {
		return claudeProcess{}, false
	}
	sess := surface.Session{
		ID:          conversation,
		Bridge:      bridge,
		Surface:     surface.KindClaude,
		RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd:         str(m, "cwd"),
		Status:      surface.SessionStatus(str(m, "status")),
	}
	sess.PID = int(pid)
	if socket != "" {
		sess.Transport = "uds"
	}
	if hosted {
		sess.RuntimeKind = surface.RuntimeClaudeHost
	}
	if n, ok := m["name"].(string); ok {
		sess.Name = n
	}
	sess.Transcript = claudeRootTranscript(transcripts(conversation), record.root)
	sess.HasLocal = sess.Transcript != "" && fileExists(sess.Transcript)
	if sess.HasLocal {
		sess.ForkedFrom = claudeForkedFrom(sess.Transcript)
	}
	if sess.Name == "" {
		sess.Name = c.firstUserMessage(sess.Transcript)
	}
	c.applyRecordStatus(ctx, &sess, m)
	if sess.HasLocal {
		if rollup, rollupErr := c.subagents.rollup(ctx, &sess); rollupErr == nil {
			sess.Subagents = rollup
		}
	}
	startedAt, ok := m["startedAt"].(float64)
	if ok {
		sess.StartedAt = time.UnixMilli(int64(startedAt))
	} else {
		startedAt = math.MaxFloat64
	}
	return claudeProcess{session: sess, startedAt: startedAt}, true
}

// applyRecordStatus takes status and last activity from Claude's session
// record. The transcript decides only when the record does not report status.
func (c *Claude) applyRecordStatus(ctx context.Context, sess *surface.Session, record map[string]any) {
	if ts, ok := record["updatedAt"].(float64); ok && ts > 0 {
		sess.LastActive = time.UnixMilli(int64(ts))
	}
	sess.Status = claudePeerStatus(record)
	sess.WaitingOn = nil
	if sess.HasLocal {
		if observation, observeErr := c.Observe(ctx, sess); observeErr == nil && observation.Status != surface.StatusUnknown {
			sess.Status = observation.Status
			sess.WaitingOn = observation.WaitingOn
		}
	}
}

// claudeRootTranscript returns a conversation's one transcript when it lies
// under root.
func claudeRootTranscript(paths []string, root string) string {
	path := onlyTranscript(paths)
	if path == "" || claudeTranscriptRoot(path) != root {
		return ""
	}
	return path
}

// pidRecord reads the session record Claude process pid wrote, from the
// config root that holds it. When records for the pid sit in more than one
// root, the one its live process wrote wins.
func (c *Claude) pidRecord(pid int) (string, map[string]any, bool) {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil || pid <= 0 {
		return "", nil, false
	}
	paths := surface.ClaudeSessionRecordPaths(roots, strconv.Itoa(pid))
	var firstPath string
	var first map[string]any
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var record map[string]any
		if json.Unmarshal(raw, &record) != nil {
			continue
		}
		if len(paths) == 1 || claudeRecordLive(record) {
			return path, record, true
		}
		if first == nil {
			firstPath, first = path, record
		}
	}
	return firstPath, first, first != nil
}

func (c *Claude) peerRecordPath(pid int) string {
	if path, _, ok := c.pidRecord(pid); ok {
		return path
	}
	return filepath.Join(surface.ClaudeConfigDir(c.home), "sessions", strconv.Itoa(pid)+".json")
}

func (c *Claude) LocalStatusFiles(session surface.Session) []string {
	if session.PID <= 0 {
		return nil
	}
	files := []string{c.peerRecordPath(session.PID)}
	if session.HasLocal && session.Transcript != "" {
		files = append(files, session.Transcript)
	}
	return files
}

func (c *Claude) LocalStatus(ctx context.Context, session surface.Session) (surface.Session, error) {
	if session.PID <= 0 {
		return session, fmt.Errorf("claude session %s has no peer process", session.ID)
	}
	record, live := c.liveRecord(session)
	if !live {
		return session, fmt.Errorf("claude pid %d no longer runs session %s", session.PID, session.ID)
	}
	c.applyRecordStatus(ctx, &session, record)
	return session, nil
}

func claudeStatus(status surface.SessionStatus) surface.SessionStatus {
	switch strings.ToLower(string(status)) {
	case "idle", "shell":
		return surface.StatusIdle
	case "busy", "running", "active", "in_progress", "inprogress", "waiting":
		return surface.StatusBusy
	case "offline":
		return surface.StatusOffline
	default:
		return surface.StatusUnknown
	}
}

// claudePeerStatus reads a Claude session record's status. Claude writes
// "shell" when its prompt is ready but a background shell is still running,
// and "waiting" while the turn is blocked on the user.
func claudePeerStatus(record map[string]any) surface.SessionStatus {
	switch str(record, "status") {
	case "busy", "waiting":
		return surface.StatusBusy
	case "idle", "shell":
	default:
		return surface.StatusUnknown
	}
	features, _ := record["peerFeatures"].([]any)
	for _, feature := range features {
		if feature == "notify_idle" {
			return surface.StatusIdle
		}
	}
	return surface.StatusUnknown
}

func (c *Claude) Resolve(ctx context.Context, target string) (*surface.Session, error) {
	sessions, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(target)
	var matches []surface.Session
	var exactMatches []surface.Session
	for _, s := range sessions {
		if strconv.Itoa(s.PID) == target ||
			s.ID == target ||
			strings.TrimSuffix(filepath.Base(s.Transcript), ".jsonl") == target {
			exactMatches = append(exactMatches, s)
			continue
		}
		if strings.HasPrefix(s.ID, target) ||
			strings.Contains(strings.ToLower(s.Cwd), lower) ||
			strings.Contains(strings.ToLower(s.Name), lower) {
			matches = append(matches, s)
		}
	}
	if len(exactMatches) > 0 {
		matches = exactMatches
	}
	if len(matches) == 0 {
		if process, found, err := c.processByPID(ctx, target); err != nil || found {
			return process, err
		}
		return nil, fmt.Errorf("%w '%s'", surface.ErrSessionNotFound, target)
	}
	if len(matches) > 1 {
		var lines []string
		for _, m := range matches {
			lines = append(lines, fmt.Sprintf("  pid=%d %s cwd=%s", m.PID, m.ID, m.Cwd))
		}
		return nil, fmt.Errorf("ambiguous target '%s':\n%s", target, strings.Join(lines, "\n"))
	}
	return &matches[0], nil
}

// processByPID finds a live process that shares its session id with an
// earlier one, which List leaves out.
func (c *Claude) processByPID(ctx context.Context, target string) (*surface.Session, bool, error) {
	pid, err := strconv.Atoi(target)
	if err != nil || pid <= 0 {
		return nil, false, nil
	}
	processes, err := c.processes(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, process := range processes {
		if process.session.PID == pid {
			session := process.session
			return &session, true, nil
		}
	}
	return nil, false, nil
}

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
	}
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
	if waiting != nil && observation.Status != surface.StatusOffline {
		observation.Status = surface.StatusBusy
		observation.WaitingOn = waiting
	}
	return observation, nil
}

// recordWaiting reads what the session's live process reports it is waiting
// on: "status":"waiting" with waitingFor "input needed" for AskUserQuestion,
// and "permission prompt" for a plan approval or a tool permission.
// statusUpdatedAt is when it started waiting, so it names one question.
func (c *Claude) recordWaiting(sess *surface.Session) *surface.Waiting {
	record, live := c.liveRecord(*sess)
	if !live || str(record, "status") != "waiting" {
		return nil
	}
	kind := surface.WaitingApproval
	if str(record, "waitingFor") == "input needed" {
		kind = surface.WaitingQuestion
	}
	since, _ := record["statusUpdatedAt"].(float64)
	waiting := &surface.Waiting{Kind: kind, ID: fmt.Sprintf("%s:%s:%d", sess.ID, kind, int64(since))}
	if since > 0 {
		waiting.Since = time.UnixMilli(int64(since)).UTC()
	}
	return waiting
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
		observation.Reply = &surface.ReplyResult{Text: state.completed.Assistant, UserText: state.completed.User, Done: true}
	}
	return observation, nil
}

func (c *Claude) Send(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	if err := peerSlashCommand(sess, message); err != nil {
		return nil, err
	}
	if err := c.EnsureWritable(ctx, sess); err != nil {
		return nil, err
	}
	if _, err := c.reach(ctx, sess); err != nil {
		return nil, err
	}
	if sess.Transport == "uds" {
		if err := peerSlashCommand(sess, message); err != nil {
			return nil, err
		}
		return c.sendPeer(ctx, sess, message)
	}
	return c.postWhenIdle(ctx, sess, message, nil)
}

// peerSlashCommand rejects a slash command for the peer socket, which cannot
// run one. It is checked before reaching the session and again on the
// transport the session was reached through.
func peerSlashCommand(sess *surface.Session, message string) error {
	if sess.Transport == "uds" && strings.HasPrefix(strings.TrimSpace(message), "/") {
		return surface.DeliveryTerminal(fmt.Errorf("Claude peer messages cannot execute slash commands; use the Claude session directly"), surface.DeliveryInvalidRequest)
	}
	return nil
}

func (c *Claude) postWhenIdle(ctx context.Context, sess *surface.Session, message string, images surface.Uploads) (*surface.SendResult, error) {
	observation, err := c.Observe(ctx, sess)
	if err != nil {
		return nil, err
	}
	if observation.Status != surface.StatusIdle || observation.ActiveTurnID != "" {
		return &surface.SendResult{UUID: sess.ID, Accepted: false}, nil
	}
	content, err := claudeUserContent(message, images)
	if err != nil {
		return nil, err
	}
	return c.postContent(ctx, sess, content)
}

// NativeImages is true only for a person's turn over the web bridge, which
// carries Messages API content blocks. The peer socket carries text.
func (c *Claude) NativeImages(_ *surface.Session, route surface.Route) bool {
	return route == surface.RouteHumanTurn
}

// claudeUserContent is the user message content: plain text, or image
// blocks followed by the text when the turn carries images.
func claudeUserContent(message string, images surface.Uploads) (any, error) {
	if len(images) == 0 {
		return message, nil
	}
	blocks := make([]map[string]any, 0, len(images)+1)
	for _, image := range images {
		data, err := os.ReadFile(image.Path)
		if err != nil {
			return nil, surface.DeliveryTerminal(fmt.Errorf("read attached image %s: %w", image.Name, err), surface.DeliveryInvalidRequest)
		}
		blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": image.MediaType, "data": base64.StdEncoding.EncodeToString(data)}})
	}
	if message != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": message})
	}
	return blocks, nil
}

// Route sends a person's message as their own turn through the session host or the
// session's web bridge identity, the routes that record it as the user's turn. A
// session with no live process resumes in the host, so a person's message to it
// is a turn too. Other messages to a session with a native peer socket take that
// socket.
func (c *Claude) Route(sess *surface.Session, author surface.Author) surface.Route {
	if author == surface.AuthorPerson && c.canSendHumanTurn(sess) {
		return surface.RouteHumanTurn
	}
	if sess != nil && sess.Transport == "uds" {
		return surface.RoutePeer
	}
	return surface.RouteSend
}

func (c *Claude) canSendHumanTurn(sess *surface.Session) bool {
	return sess != nil && sess.Surface == surface.KindClaude && (claudeHosted(sess) || !claudeProcessAlive(sess.PID) || !c.localOnly && !nativeClaudeOnly(sess))
}

// SendHumanTurn submits a message through Claude's authenticated web bridge as
// a user event, even when the session also has a native peer socket.
func (c *Claude) SendHumanTurn(ctx context.Context, sess *surface.Session, message string, images surface.Uploads) (*surface.SendResult, error) {
	if sess == nil {
		return nil, surface.DeliveryTerminal(errors.New("Claude session is required"), surface.DeliveryInvalidRequest)
	}
	if err := c.EnsureWritable(ctx, sess); err != nil {
		return nil, err
	}
	if !c.canSendHumanTurn(sess) {
		return nil, surface.DeliveryUnavailable(errors.New("Claude web bridge session is unavailable"))
	}
	if _, err := c.reach(ctx, sess); err != nil {
		return nil, err
	}
	if claudeHosted(sess) {
		return c.sendHostTurn(ctx, sess, message, "", images)
	}
	if !c.canSendHumanTurn(sess) {
		return nil, surface.DeliveryUnavailable(errors.New("Claude resumed without its web bridge; the message is routed again on retry"))
	}
	return c.postWhenIdle(ctx, sess, message, images)
}

func claudeProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
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

var claudeSendRequest = sidecarRequestWithCookies

func (c *Claude) postMessage(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	return c.postContent(ctx, sess, message)
}

func (c *Claude) postContent(ctx context.Context, sess *surface.Session, content any) (*surface.SendResult, error) {
	if c.localOnly {
		return nil, surface.DeliveryUnavailable(errors.New("Claude Remote Control is not available on this install; reach the session through its local Claude Code process"))
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if sess.Bridge == "" {
		return nil, surface.DeliveryUnavailable(fmt.Errorf("Claude conversation %s has no Remote Control attachment", sess.ID))
	}
	cookies, err := c.cookieSource(sess.ID)
	if err != nil {
		return nil, surface.DeliveryTerminal(err, signinTerminalKind(err))
	}
	uuid := newUUID()
	cse := toCse(sess.Bridge)
	body := map[string]any{
		"events": []map[string]any{{
			"payload": map[string]any{
				"type": "user", "uuid": uuid, "session_id": sess.Bridge,
				"parent_tool_use_id": nil,
				"message":            map[string]any{"role": "user", "content": content},
			},
		}},
	}
	bodyBytes, _ := json.Marshal(body)
	headers := c.headerMap("", sess.Bridge)
	headers["content-type"] = "application/json"
	request := c.request
	if request == nil {
		request = claudeSendRequest
	}
	status, respBody, err := request(ctx, "POST",
		"https://claude.ai/v1/code/sessions/"+cse+"/events",
		headers, string(bodyBytes), cookies, 30*time.Second)
	if surface.IsDeliveryUnavailable(err) {
		return nil, err
	}
	if err != nil {
		return nil, surface.DeliveryOutcomeUnknown(err)
	}
	if strings.Contains(respBody, "Just a moment") {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("cloudflare challenge (cf_clearance may need refresh)"))
	}
	switch status {
	case http.StatusBadRequest:
		return nil, surface.DeliveryTerminal(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)), surface.DeliveryInvalidRequest)
	case http.StatusUnauthorized:
		return nil, surface.DeliveryTerminal(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)), surface.DeliveryAuthenticationNeeded)
	case http.StatusForbidden:
		return nil, surface.DeliveryTerminal(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)), surface.DeliveryAccessDenied)
	case http.StatusNotFound:
		return nil, surface.DeliveryTerminal(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)), surface.DeliveryTargetMissing)
	}
	if status != http.StatusOK {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)))
	}
	return &surface.SendResult{UUID: uuid, Accepted: true}, nil
}

func (c *Claude) Reply(ctx context.Context, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	read, err := c.ReadSession(ctx, sess, surface.SessionReadRequest{Limit: max(1, limit)})
	if err != nil {
		return nil, err
	}
	if read.Reply == nil {
		return &surface.ReplyResult{Done: false, Source: read.Source}, nil
	}
	return read.Reply, nil
}

func (c *Claude) Stream(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	if sess.Transport == "uds" && uuid != "" {
		return surface.ErrUnsupported
	}
	return c.streamTimeline(ctx, sess, uuid, onEvent, timeout)
}

func (c *Claude) streamTimeline(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	path, err := c.sessionTranscript(sess)
	if err != nil {
		return err
	}
	if path == "" || !fileExists(path) {
		return fmt.Errorf("no local transcript for streaming")
	}
	state, err := c.observeTranscript(ctx, path, sess)
	if err != nil {
		return err
	}
	identity := transcriptFileIdentity(state.fileInfo)
	offset := state.offset
	var closing surface.ClaudeClosingTurn
	if uuid != "" {
		// A targeted wait looks back so a turn submitted before the tail started is still found;
		// events outside that turn are filtered below.
		offset = max(int64(0), state.offset-initialClaudeObservationBytes)
	} else if sess.TranscriptOffsetSet && sess.TranscriptOffset <= state.offset && identity != "" && sess.TranscriptIdentity == identity {
		// The session source resumes at its seed or previous window boundary, so records written
		// in between are delivered once without replaying history the seed bounded away.
		offset = sess.TranscriptOffset
		closing = sess.ClaudeClosingTurn
	}
	if uuid != "" && offset > 0 {
		file, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		if _, seekErr := file.Seek(offset, 0); seekErr != nil {
			file.Close()
			return seekErr
		}
		reader := bufio.NewReader(file)
		prefix, readErr := reader.ReadBytes('\n')
		file.Close()
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		offset += int64(len(prefix))
	}
	deadline := time.Now().Add(timeout)
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	replayed, err := claudeTurnStateAt(ctx, file, offset)
	file.Close()
	if err != nil {
		return err
	}
	turns := newClaudeTurnTracker(replayed.current, closing)
	completed := false
	finish := func(ended []claudeTurnEnd) {
		for _, end := range ended {
			if uuid != "" && end.TurnID != uuid && end.Status == "" {
				continue
			}
			onEvent(surface.StreamEvent{ID: end.Key, ProviderKey: "timeline:" + end.Key, Version: 1, Operation: "phase", TurnID: end.TurnID, Kind: "done", Status: end.Status})
			if uuid != "" && end.TurnID == uuid && end.Status == "" {
				completed = true
			}
		}
	}
	for time.Now().Before(deadline) {
		// A closing turn belongs to the bytes before offset; a rewritten transcript drops it.
		// The anchor then rolls forward over the bytes this scan parses, never a later read.
		tail, tailErr := transcriptTail(path, offset)
		if turns.closing.TurnID != "" && (tailErr != nil || anchorOf(tail) != turns.closing.Anchor) {
			turns.closing = surface.ClaudeClosingTurn{}
		}
		lineOffset := offset
		next, scanErr := scanAppendedJSONL(ctx, path, offset, maxClaudeTranscriptRecordBytes, func(line []byte) error {
			recordOffset := lineOffset
			lineOffset += int64(len(line))
			tail = append(tail, line...)
			if len(tail) > transcriptAnchorBytes {
				tail = append(tail[:0], tail[len(tail)-transcriptAnchorBytes:]...)
			}
			var record map[string]any
			if json.Unmarshal(line, &record) != nil {
				return nil
			}
			items := claudeTimelineItems(record)
			turnID, ended, interrupted := turns.observe(record, recordOffset, line, len(items))
			if interrupted && uuid != "" && turnID == uuid {
				return fmt.Errorf("Claude turn %s was interrupted", uuid)
			}
			finish(ended)
			if interrupted {
				return nil
			}
			if err := decorateTimelineAttachments(ctx, items, record, "claude", recordOffset); err != nil {
				return err
			}
			if uuid != "" && turnID != uuid {
				return nil
			}
			for index, item := range items {
				key := item.ID
				if key == "" {
					key = stableTimelineItemID(recordOffset, line, index)
				}
				version := uint64(len(item.Text))
				if version == 0 {
					version = 1
				}
				at, _ := time.Parse(time.RFC3339Nano, str(record, "timestamp"))
				onEvent(surface.StreamEvent{ID: key, ProviderKey: "timeline:" + key, Version: version, Operation: "upsert", Final: true, TurnID: turnID, Role: item.Role, Origin: item.Origin, Sender: item.Sender, Title: item.Title, CallID: item.CallID, Status: item.Status, Attachment: item.Attachment, Files: item.Files, Truncated: item.Truncated, TruncationReason: item.TruncationReason, Timestamp: at, Kind: item.Kind, Text: item.Text})
			}
			return nil
		})
		if scanErr != nil {
			return scanErr
		}
		offset = next
		finish(turns.settle(func(at time.Time) bool { return c.settledAfter(sess, at) }))
		if turns.closing.TurnID != "" {
			turns.closing.Anchor = anchorOf(tail)
		}
		if uuid == "" {
			sess.TranscriptOffset = offset
			sess.TranscriptOffsetSet = true
			sess.TranscriptIdentity = identity
			sess.ClaudeClosingTurn = turns.closing
		}
		if completed {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(transcriptPollInterval):
		}
	}
	return fmt.Errorf("stream timed out after %s: %w", timeout, surface.ErrStreamWindow)
}

func claudeRecordStartsTurn(record map[string]any) bool {
	return str(record, "type") == "user" && str(record, "uuid") != "" && !claudeRecordHasToolResult(record) && !claudeRecordIsInterrupt(record)
}

func claudeRecordHasToolResult(record map[string]any) bool {
	message, _ := record["message"].(map[string]any)
	blocks, _ := message["content"].([]any)
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if str(block, "type") == "tool_result" {
			return true
		}
	}
	return str(message, "tool_use_id") != ""
}

func claudeRecordIsInterrupt(record map[string]any) bool {
	if str(record, "type") != "user" {
		return false
	}
	message, _ := record["message"].(map[string]any)
	return isClaudeInterruptMarker(strings.TrimSpace(transcriptText(message["content"])))
}

// claudeStreamTerminal classifies a record: turn_duration ends a turn, an
// end_turn block starts closing it (see appendClaudeTurn), and an interruption
// cancels it.
func claudeStreamTerminal(record map[string]any) (done, ending, interrupted bool) {
	switch str(record, "type") {
	case "system":
		return str(record, "subtype") == "turn_duration", false, false
	case "assistant":
		reason := strNested(record, "message", "stop_reason")
		return false, reason == "end_turn", claudeTerminalInterruption(reason)
	case "user":
		return false, false, claudeRecordIsInterrupt(record)
	default:
		return false, false, false
	}
}

func strNested(record map[string]any, object, field string) string {
	value, _ := record[object].(map[string]any)
	return str(value, field)
}

func (c *Claude) GoalSet(ctx context.Context, sess *surface.Session, text string) error {
	return surface.ErrUnsupported
}

func (c *Claude) GoalClear(ctx context.Context, sess *surface.Session) error {
	return surface.ErrUnsupported
}

func (c *Claude) GoalGet(ctx context.Context, sess *surface.Session) (*surface.GoalState, error) {
	return nil, surface.ErrUnsupported
}

func (c *Claude) Compact(ctx context.Context, sess *surface.Session) error {
	_, err := c.Control(ctx, sess, surface.ControlRequest{Action: "compact"})
	return err
}

func (c *Claude) Model(ctx context.Context, sess *surface.Session, name string) (string, error) {
	if err := c.EnsureWritable(ctx, sess); err != nil {
		return "", err
	}
	if name != "" {
		result, err := c.Control(ctx, sess, surface.ControlRequest{Action: "model", Value: name})
		if err != nil {
			return "", fmt.Errorf("model switch rejected: %w", err)
		}
		return result.Output, nil
	}
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	return readClaudeLatestModel(ctx, path)
}

func (c *Claude) Interrupt(ctx context.Context, sess *surface.Session) error {
	_, err := c.Control(ctx, sess, surface.ControlRequest{Action: "interrupt"})
	return err
}

func (c *Claude) Steer(ctx context.Context, sess *surface.Session, message string) error {
	_, err := c.Control(ctx, sess, surface.ControlRequest{Action: "steer", Value: message})
	return err
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (c *Claude) Tail(ctx context.Context, sess *surface.Session, n int) ([]surface.Exchange, error) {
	read, err := c.ReadSession(ctx, sess, surface.SessionReadRequest{Limit: n})
	if err != nil {
		return nil, err
	}
	return read.Exchanges, nil
}
