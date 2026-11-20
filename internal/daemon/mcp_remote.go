package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/cfaccess"
	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	remoteInboxURIPrefix = "agenthail://inbox/"
	remoteInboxSweep     = 5 * time.Second
	remoteTokenLife      = time.Hour
	remoteSessionIdle    = time.Hour
	tokenInfoGeneration  = "agenthail.generation"
	tokenInfoRemote      = "agenthail.remote"
	tokenInfoBinding     = "agenthail.binding"
)

type remoteMCP struct {
	d       *Daemon
	server  *mcp.Server
	handler http.Handler

	verifierMu   sync.Mutex
	verifier     *cfaccess.Verifier
	verifierKey  string
	keysRecorded string
	// certsURL and certsClient replace the team's key set, for tests.
	certsURL    string
	certsClient *http.Client

	mu       sync.Mutex
	sessions map[*mcp.ServerSession]*remoteMCPSession
	wake     chan struct{}
}

type remoteMCPSession struct {
	identity   string
	name       string
	generation string
	notified   int64
}

func remoteInboxURI(name string) string { return remoteInboxURIPrefix + name }

func (d *Daemon) remoteMCP() *remoteMCP {
	d.remoteOnce.Do(func() {
		m := &remoteMCP{d: d, sessions: map[*mcp.ServerSession]*remoteMCPSession{}, wake: make(chan struct{}, 1)}
		m.server = mcp.NewServer(&mcp.Implementation{Name: "agenthail", Version: d.build.Version}, &mcp.ServerOptions{
			SupportedProtocolVersions: mcpProtocolVersions,
			SubscribeHandler:          m.subscribe,
			UnsubscribeHandler:        func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
			Instructions:              "You are an Agenthail agent identity. Messages sent to you wait in your inbox, the resource " + remoteInboxURIPrefix + "<your name>; subscribe to it to be told when one arrives. agenthail_read with no target returns your unread messages and marks them read; agenthail_wait with no tasks returns when one arrives. Reply with agenthail_send to the sender it names.",
		})
		m.server.AddReceivingMiddleware(m.track)
		d.registerMCPTools(m.server)
		m.server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: remoteInboxURIPrefix + "{name}", Name: "inbox", Title: "Inbox", Description: "Your unread messages, oldest first. Reading the resource leaves them unread.", MIMEType: "application/json"}, m.readInbox)
		stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return m.server }, &mcp.StreamableHTTPOptions{EventStore: mcp.NewMemoryEventStore(nil), SessionTimeout: cmp.Or(d.remoteIdle, remoteSessionIdle)})
		bearer := auth.RequireBearerToken(m.verify, &auth.RequireBearerTokenOptions{})
		m.handler = bearer(http.NewCrossOriginProtection().Handler(stream))
		d.remote = m
	})
	return d.remote
}

func remoteRequest(r *http.Request) bool {
	return r.Header.Get(cfaccess.Header) != "" || strings.HasPrefix(bearerToken(r), registry.RemoteTokenPrefix)
}

// A tunnel that publishes more than /mcp still exposes nothing else.
func accessPathGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(cfaccess.Header) != "" && r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *remoteMCP) accessVerifier(team, aud string) *cfaccess.Verifier {
	m.verifierMu.Lock()
	defer m.verifierMu.Unlock()
	if key := team + "\x00" + aud; m.verifier == nil || m.verifierKey != key {
		m.verifier, m.verifierKey = cfaccess.New(team, aud, m.certsURL, m.certsClient), key
	}
	return m.verifier
}

// Doctor runs in another process, so the key-set status goes to the registry.
func (m *remoteMCP) recordKeys(verifier *cfaccess.Verifier) {
	readAt, readErr := verifier.Status()
	m.verifierMu.Lock()
	key := readAt.String()
	if readErr != nil {
		key += readErr.Error()
	}
	changed := key != m.keysRecorded
	m.keysRecorded = key
	m.verifierMu.Unlock()
	if changed {
		if err := m.d.Registry.RecordAccessKeys(readAt, readErr); err != nil {
			m.d.logRuntimeError("record the Access key read", err)
		}
	}
}

type remoteRefusal struct{ reason string }

func (e remoteRefusal) Error() string { return e.reason }

func (m *remoteMCP) verify(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	d := m.d
	refuse := func(reason string) (*auth.TokenInfo, error) {
		d.logMCPAuthRefusal("remote", "-", "unauthenticated", remoteRefusal{reason})
		return nil, fmt.Errorf("%w: %s", auth.ErrInvalidToken, reason)
	}
	remote, err := d.Registry.RemoteIdentityByToken(token)
	if errors.Is(err, registry.ErrRemoteUnknown) {
		return refuse("the bearer is not a remote token of any identity here")
	}
	if err != nil {
		return nil, err
	}
	team, aud, configured, err := d.Registry.AccessSettings()
	if err != nil {
		return nil, err
	}
	assertion := r.Header.Get(cfaccess.Header)
	binding := "token only"
	switch {
	case configured && assertion == "":
		return refuse("Access is configured here, and this request did not come through it")
	case configured:
		verifier := m.accessVerifier(team, aud)
		claims, err := verifier.Verify(ctx, assertion)
		m.recordKeys(verifier)
		if err != nil {
			return refuse(err.Error())
		}
		switch {
		case claims.Subject == "" && remote.ClientID != "" && cfaccess.ClientID(claims.CommonName) == remote.ClientID:
			binding = "service token " + remote.ClientID
		case claims.Subject != "" && remote.Email != "" && strings.EqualFold(strings.TrimSpace(claims.Email), remote.Email):
			binding = "email " + remote.Email
		default:
			return refuse(fmt.Sprintf("the Access credential is not the one bound to @%s", remote.Name))
		}
	case assertion != "":
		return refuse("this request came through Access, and no Access settings are set here; run agenthail identity access set")
	}
	if err := d.Registry.TouchRemoteIdentity(remote.IdentityID, time.Now()); err != nil {
		d.logRuntimeError("remote mcp last seen", err)
	}
	extra := map[string]any{tokenInfoRemote: remote.Name, tokenInfoBinding: binding, tokenInfoGeneration: remoteGeneration(remote, team, aud, configured)}
	return &auth.TokenInfo{UserID: remote.IdentityID, Expiration: time.Now().Add(remoteTokenLife), Extra: extra}, nil
}

// A rotation, rebind, --off or Access change ends sessions opened before it.
func remoteGeneration(remote registry.RemoteIdentity, team, aud string, configured bool) string {
	return fmt.Sprintf("%d|%s|%s|%t|%s|%s", remote.CreatedAt.UnixMilli(), remote.ClientID, remote.Email, configured, team, aud)
}

func (m *remoteMCP) currentGenerations() (map[string]string, error) {
	remotes, err := m.d.Registry.RemoteIdentities()
	if err != nil {
		return nil, err
	}
	team, aud, configured, err := m.d.Registry.AccessSettings()
	if err != nil {
		return nil, err
	}
	current := map[string]string{}
	for _, remote := range remotes {
		current[remote.IdentityID] = remoteGeneration(remote, team, aud, configured)
	}
	return current, nil
}

// An identity has the person's reach, as the CLI it sends with does.
func (d *Daemon) remoteCaller(info *auth.TokenInfo) (*mcpCaller, error) {
	if info == nil {
		return nil, nil
	}
	name, ok := info.Extra[tokenInfoRemote].(string)
	if !ok {
		return nil, nil
	}
	session, err := d.Registry.Session(info.UserID)
	if err != nil {
		return nil, fmt.Errorf("read @%s: %w", name, err)
	}
	caller, err := d.agentCaller(*session)
	if err != nil {
		return nil, err
	}
	for _, capability := range operatorCapabilities {
		if !caller.has(capability) {
			caller.capabilities = append(caller.capabilities, capability)
		}
	}
	caller.remote, _ = info.Extra[tokenInfoBinding].(string)
	return caller, nil
}

func remoteName(extra *mcp.RequestExtra) (string, *auth.TokenInfo) {
	if extra == nil || extra.TokenInfo == nil {
		return "", nil
	}
	name, _ := extra.TokenInfo.Extra[tokenInfoRemote].(string)
	return name, extra.TokenInfo
}

// The SDK's InitializedHandler gets no TokenInfo, so sessions are learned here.
func (m *remoteMCP) track(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		session, ok := req.GetSession().(*mcp.ServerSession)
		name, info := remoteName(req.GetExtra())
		if !ok || name == "" || method == "initialize" {
			return next(ctx, method, req)
		}
		m.mu.Lock()
		_, known := m.sessions[session]
		if !known {
			generation, _ := info.Extra[tokenInfoGeneration].(string)
			m.sessions[session] = &remoteMCPSession{identity: info.UserID, name: name, generation: generation}
		}
		m.mu.Unlock()
		result, err := next(ctx, method, req)
		// The SDK registers a subscription or log level only once the handler returns.
		if err == nil && (method == "resources/subscribe" || method == "logging/setLevel") {
			m.mu.Lock()
			if state, ok := m.sessions[session]; ok {
				state.notified = 0
			}
			m.mu.Unlock()
		}
		if !known || err == nil && (method == "resources/subscribe" || method == "logging/setLevel") {
			m.poke()
		}
		return result, err
	}
}

func (m *remoteMCP) subscribe(_ context.Context, req *mcp.SubscribeRequest) error {
	name, _ := remoteName(req.Extra)
	if name == "" || req.Params == nil {
		return errors.New("only a remote identity subscribes to an inbox")
	}
	if req.Params.URI != remoteInboxURI(name) {
		return fmt.Errorf("you may subscribe only to your own inbox, %s", remoteInboxURI(name))
	}
	return nil
}

func (m *remoteMCP) readInbox(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	name, _ := remoteName(req.Extra)
	if name == "" || req.Params.URI != remoteInboxURI(name) {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	messages, err := m.d.Registry.ReadInbox(name, registry.InboxQuery{Peek: true})
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"identity": name, "unread": messages})
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(body)}}}, nil
}

func (m *remoteMCP) poke() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// The inbox is the record; a new or resubscribed session is told of all unread.
func (d *Daemon) runRemoteInboxNotices(ctx context.Context) {
	m := d.remoteMCP()
	var follow <-chan struct{}
	if path, err := d.Registry.ChangeSignal(registry.ChangeAgentInbox); err != nil {
		d.log.Printf("follow agent inboxes, noticing them every %s instead: %s", remoteInboxSweep, err)
	} else {
		wake := filewake.Follow(path)
		defer wake.Release()
		follow = wake.C()
	}
	ticker := time.NewTicker(remoteInboxSweep)
	defer ticker.Stop()
	for {
		m.notify(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.wake:
		case <-follow:
		}
	}
}

func (m *remoteMCP) notify(ctx context.Context) {
	live := map[*mcp.ServerSession]bool{}
	for session := range m.server.Sessions() {
		live[session] = true
	}
	m.mu.Lock()
	for session := range m.sessions {
		if !live[session] {
			delete(m.sessions, session)
		}
	}
	if len(m.sessions) == 0 {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	current, err := m.currentGenerations()
	if err != nil {
		m.d.logRuntimeError("remote inbox notices", err)
		return
	}
	var revoked []*mcp.ServerSession
	m.mu.Lock()
	for session, state := range m.sessions {
		if current[state.identity] != state.generation {
			revoked = append(revoked, session)
			delete(m.sessions, session)
		}
	}
	m.mu.Unlock()
	for _, session := range revoked {
		_ = session.Close()
	}
	latest, err := m.d.Registry.LatestUnreadInbox()
	if err != nil {
		m.d.logRuntimeError("remote inbox notices", err)
		return
	}
	type due struct {
		session *mcp.ServerSession
		state   remoteMCPSession
		newest  int64
	}
	var notices []due
	names := map[string]bool{}
	m.mu.Lock()
	for session, state := range m.sessions {
		if newest := latest[state.identity]; newest > state.notified {
			notices = append(notices, due{session: session, state: *state, newest: newest})
			names[state.name] = true
			state.notified = newest
		}
	}
	m.mu.Unlock()
	for name := range names {
		if err := m.server.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: remoteInboxURI(name)}); err != nil {
			m.d.logRuntimeError("remote inbox resource updated", err)
		}
	}
	for _, notice := range notices {
		messages, err := m.d.Registry.ReadInbox(notice.state.name, registry.InboxQuery{Peek: true})
		if err != nil || len(messages) == 0 {
			continue
		}
		last := messages[len(messages)-1]
		data := map[string]any{"inbox": remoteInboxURI(notice.state.name), "unread": len(messages), "from": last.SenderName, "summary": clip(last.Message, 200)}
		_ = notice.session.Log(ctx, &mcp.LoggingMessageParams{Level: "notice", Logger: "agenthail", Data: data})
	}
}

func (d *Daemon) recordRemoteCall(caller *mcpCaller, tool string, refusal *mcpRefusal) {
	if caller == nil || caller.remote == "" {
		return
	}
	outcome, target := "ok", ""
	if refusal != nil {
		outcome, target = refusal.Code, refusal.target
	}
	if err := d.Registry.RecordHistory(registry.HistoryEntry{Kind: "mcp-remote", SessionID: target, SourceSessionID: caller.session.ID, Message: tool + " via " + caller.remote, Result: outcome}); err != nil {
		d.logRuntimeError("remote mcp audit", err)
	}
}

func clip(text string, limit int) string {
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}
