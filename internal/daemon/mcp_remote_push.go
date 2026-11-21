package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	remoteInboxSweep = 5 * time.Second
	remoteRetireWait = 2 * time.Second
)

type remotePushSession struct {
	identity string
	name     string
	epoch    int64
	notified int64
	// retells counts subscribes and level changes, so a pass collected before one cannot mark it told.
	retells uint64
	// Requests run under ctx, so retiring a session ends its waits and Close is not held by them.
	ctx    context.Context
	cancel context.CancelFunc
	// retiring sessions stay tracked until the SDK ends them, so they are neither bound again nor told.
	retiring bool
}

func remoteInboxURI(name string) string { return remoteInboxURIStart + name }

var errRemoteStale = errors.New("the remote credentials changed since this request was verified; reconnect")

// The SDK's InitializedHandler gets no TokenInfo, so a session binds on its first verified message once initialized.
func (m *remoteMCP) track(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		session, ok := req.GetSession().(*mcp.ServerSession)
		name, info := remoteName(req.GetExtra())
		if !ok || name == "" || session.InitializeParams() == nil {
			return next(ctx, method, req)
		}
		epoch, _ := info.Extra[tokenInfoEpoch].(int64)
		state, err := m.bind(session, info.UserID, name, epoch)
		if errors.Is(err, errRemoteStale) {
			go session.Close()
		}
		if err != nil {
			return nil, err
		}
		requestCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		defer context.AfterFunc(state.ctx, cancel)()
		result, err := next(requestCtx, method, req)
		if err == nil && (method == "resources/subscribe" || method == "logging/setLevel") {
			m.mu.Lock()
			if !state.retiring {
				state.notified = 0
				state.retells++
			}
			m.mu.Unlock()
			m.poke()
		}
		return result, err
	}
}

// One lock over the epoch read and the insert: a notice pass either sees the session or read its epoch first.
func (m *remoteMCP) bind(session *mcp.ServerSession, identity, name string, epoch int64) (*remotePushSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped.Load() {
		return nil, errRemoteStale
	}
	if state, ok := m.sessions[session]; ok {
		if state.retiring {
			return nil, errRemoteStale
		}
		return state, nil
	}
	current, err := m.d.Registry.RemoteEpoch()
	if err != nil {
		return nil, err
	}
	if epoch < current {
		return nil, errRemoteStale
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := &remotePushSession{identity: identity, name: name, epoch: epoch, ctx: ctx, cancel: cancel}
	m.sessions[session] = state
	go m.unbindWhenDone(session, state)
	m.poke()
	return state, nil
}

func (m *remoteMCP) unbindWhenDone(session *mcp.ServerSession, state *remotePushSession) {
	_ = session.Wait()
	state.cancel()
	m.mu.Lock()
	if m.sessions[session] == state {
		delete(m.sessions, session)
	}
	m.mu.Unlock()
}

// retireLocked starts retiring a bound session; m.mu must be held.
func (m *remoteMCP) retireLocked(session *mcp.ServerSession, state *remotePushSession) {
	if state.retiring {
		return
	}
	state.retiring = true
	state.cancel()
	go session.Close()
}

func retire(sessions map[*mcp.ServerSession]*remotePushSession) {
	var wg sync.WaitGroup
	for session, state := range sessions {
		if state != nil {
			state.cancel()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = session.Close()
		}()
	}
	closed := make(chan struct{})
	go func() { wg.Wait(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(remoteRetireWait):
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
	unread, err := m.d.Registry.UnreadInbox(name)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(remoteNotice(name, unread))
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(body)}}}, nil
}

// Contentless, so a notice racing a revocation reveals only that mail is waiting.
func remoteNotice(name string, unread int) map[string]any {
	return map[string]any{"inbox": remoteInboxURI(name), "unread": unread, "next": "Call agenthail_read with no sessionId to read them."}
}

func (m *remoteMCP) poke() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

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
			m.closeAll()
			return
		case <-ticker.C:
		case <-m.wake:
		case <-follow:
		}
	}
}

func (m *remoteMCP) closeAll() {
	m.admitMu.Lock()
	m.stopped.Store(true)
	m.admitMu.Unlock()
	m.stopRequests()
	drained := make(chan struct{})
	go func() { m.admitted.Wait(); close(drained) }()
	select {
	case <-drained:
		m.retireAll()
	case <-time.After(remoteRetireWait):
		m.retireAll()
		// A request still admitted may yet open a session; retire again once it has gone.
		go func() {
			<-drained
			m.retireAll()
		}()
	}
}

func (m *remoteMCP) retireAll() {
	m.mu.Lock()
	all := map[*mcp.ServerSession]*remotePushSession{}
	for session, state := range m.sessions {
		all[session] = state
	}
	m.mu.Unlock()
	for session := range m.server.Sessions() {
		if _, bound := all[session]; !bound {
			all[session] = nil
		}
	}
	retire(all)
}

func (m *remoteMCP) notify(ctx context.Context) {
	m.mu.Lock()
	idle := len(m.sessions) == 0
	m.mu.Unlock()
	if idle {
		return
	}
	epoch, err := m.d.Registry.RemoteEpoch()
	if err != nil {
		m.d.logRuntimeError("remote inbox notices", err)
		return
	}
	latest, err := m.d.Registry.LatestUnreadInbox()
	if err != nil {
		m.d.logRuntimeError("remote inbox notices", err)
		return
	}
	type due struct {
		session *mcp.ServerSession
		name    string
		newest  int64
		retells uint64
	}
	var notices []due
	m.mu.Lock()
	for session, state := range m.sessions {
		switch newest := latest[state.identity]; {
		case state.retiring:
		case state.epoch < epoch:
			m.retireLocked(session, state)
		case newest > state.notified:
			notices = append(notices, due{session: session, name: state.name, newest: newest, retells: state.retells})
		}
	}
	m.mu.Unlock()
	if len(notices) == 0 {
		return
	}
	if now, err := m.d.Registry.RemoteEpoch(); err != nil || now != epoch {
		m.poke()
		return
	}
	if m.beforeNotice != nil {
		m.beforeNotice()
	}
	unread := map[string]int{}
	for _, notice := range notices {
		if _, counted := unread[notice.name]; counted {
			continue
		}
		count, err := m.d.Registry.UnreadInbox(notice.name)
		if err != nil {
			m.d.logRuntimeError("remote inbox notices", err)
			return
		}
		unread[notice.name] = count
		if err := m.server.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: remoteInboxURI(notice.name)}); err != nil {
			m.d.logRuntimeError("remote inbox resource updated", err)
		}
	}
	for _, notice := range notices {
		// The SDK hides per-subscriber resource errors; a failed Log on the same session stands for both.
		if err := notice.session.Log(ctx, &mcp.LoggingMessageParams{Level: "notice", Logger: "agenthail", Data: remoteNotice(notice.name, unread[notice.name])}); err != nil {
			continue
		}
		m.mu.Lock()
		if state, ok := m.sessions[notice.session]; ok && !state.retiring && state.retells == notice.retells && notice.newest > state.notified {
			state.notified = notice.newest
		}
		m.mu.Unlock()
	}
}
