package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/registry"
)

const remoteInboxSweep = 5 * time.Second

type remotePushSession struct {
	identity string
	name     string
	epoch    int64
	notified int64
	// retells counts subscribes and level changes, so a pass collected before one cannot mark it told.
	retells uint64
}

func remoteInboxURI(name string) string { return remoteInboxURIStart + name }

// The SDK's InitializedHandler gets no TokenInfo, so a session binds on its first verified message once initialized.
func (m *remoteMCP) track(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		session, ok := req.GetSession().(*mcp.ServerSession)
		name, info := remoteName(req.GetExtra())
		if !ok || name == "" || session.InitializeParams() == nil {
			return next(ctx, method, req)
		}
		epoch, _ := info.Extra[tokenInfoEpoch].(int64)
		m.mu.Lock()
		_, known := m.sessions[session]
		m.mu.Unlock()
		if !known {
			current, err := m.d.Registry.RemoteEpoch()
			if err != nil {
				return nil, err
			}
			if epoch < current {
				go session.Close()
				return nil, errors.New("the remote credentials changed since this request was verified; reconnect")
			}
			m.mu.Lock()
			if _, known = m.sessions[session]; !known {
				m.sessions[session] = &remotePushSession{identity: info.UserID, name: name, epoch: epoch}
			}
			m.mu.Unlock()
			if !known {
				go m.unbindWhenDone(session)
			}
		}
		result, err := next(ctx, method, req)
		retell := err == nil && (method == "resources/subscribe" || method == "logging/setLevel")
		if retell {
			m.mu.Lock()
			if state, ok := m.sessions[session]; ok {
				state.notified = 0
				state.retells++
			}
			m.mu.Unlock()
		}
		if !known || retell {
			m.poke()
		}
		return result, err
	}
}

func (m *remoteMCP) unbindWhenDone(session *mcp.ServerSession) {
	_ = session.Wait()
	m.mu.Lock()
	delete(m.sessions, session)
	m.mu.Unlock()
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

func (m *remoteMCP) closeStale(epoch int64) {
	var stale []*mcp.ServerSession
	m.mu.Lock()
	for session, state := range m.sessions {
		if state.epoch < epoch {
			stale = append(stale, session)
			delete(m.sessions, session)
		}
	}
	m.mu.Unlock()
	for _, session := range stale {
		_ = session.Close()
	}
}

func (m *remoteMCP) closeAll() {
	m.closeStale(1 << 62)
	for session := range m.server.Sessions() {
		_ = session.Close()
	}
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
	var stale []*mcp.ServerSession
	m.mu.Lock()
	for session, state := range m.sessions {
		switch newest := latest[state.identity]; {
		case state.epoch < epoch:
			stale = append(stale, session)
			delete(m.sessions, session)
		case newest > state.notified:
			notices = append(notices, due{session: session, name: state.name, newest: newest, retells: state.retells})
		}
	}
	m.mu.Unlock()
	for _, session := range stale {
		_ = session.Close()
	}
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
		if state, ok := m.sessions[notice.session]; ok && state.retells == notice.retells && notice.newest > state.notified {
			state.notified = notice.newest
		}
		m.mu.Unlock()
	}
}
