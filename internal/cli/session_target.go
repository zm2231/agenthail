package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) resolveSession(ctx context.Context, kind surface.SurfaceKind, adapter surface.Surface, selector, target string) (session *surface.Session, live bool, err error) {
	session, err = adapter.Resolve(ctx, selector)
	if err == nil || a.registry() == nil {
		return session, err == nil, err
	}
	if surface.IsDeliveryUnavailable(err) {
		sid, lookupErr := a.registry().ResolveSurfaceTarget(kind, selector)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return nil, false, err
		}
		if lookupErr != nil {
			return nil, false, lookupErr
		}
		registered, lookupErr := a.registry().Session(sid)
		return registered, false, lookupErr
	}
	if !errors.Is(err, surface.ErrSessionNotFound) {
		return nil, false, err
	}
	sid, exactErr := a.registry().ExactTarget(kind, target)
	if exactErr != nil || (selector != target && sid != selector) {
		return nil, false, a.exitedNameError(kind, target, err)
	}
	registered, lookupErr := a.registry().Session(sid)
	if lookupErr != nil || registered.Surface != kind {
		return nil, false, err
	}
	return registered, false, nil
}

type exitedNamedSession struct {
	kind     surface.SurfaceKind
	target   string
	sessions []registry.NamedSession
	cause    error
}

func (e exitedNamedSession) Error() string {
	handles := make([]string, 0, len(e.sessions))
	for _, session := range e.sessions {
		handle := session.ID
		if session.Alias != "" {
			handle += " (@" + session.Alias + ")"
		}
		handles = append(handles, handle)
	}
	return fmt.Sprintf("%q is the name of an exited %s session, and a name does not resume one; address it by id or alias: %s", e.target, e.kind, strings.Join(handles, ", "))
}

func (e exitedNamedSession) Unwrap() error { return e.cause }

func (a *App) exitedNameError(kind surface.SurfaceKind, target string, cause error) error {
	sessions, err := a.registry().SessionsNamed(kind, strings.TrimPrefix(target, "@"))
	if err != nil || len(sessions) == 0 {
		return cause
	}
	return exitedNamedSession{kind: kind, target: target, sessions: sessions, cause: cause}
}

func (a *App) resolveLocalTarget(ctx context.Context, target string) (*surface.Session, surface.Surface, error) {
	if a.registry() != nil {
		if registered, err := a.registry().Session(target); err == nil {
			if err := surface.NoReplyRoute(registered); err != nil {
				return nil, nil, err
			}
		}
	}
	if kindText, selector, ok := strings.Cut(target, ":"); ok {
		kind := surface.SurfaceKind(strings.ToLower(kindText))
		adapter := a.adapters().ForSurface(kind)
		if adapter == nil {
			return nil, nil, fmt.Errorf("unknown surface %q", kindText)
		}
		session, live, err := a.resolveSession(ctx, kind, adapter, selector, selector)
		if err != nil {
			return nil, nil, fmt.Errorf("%s target: %w", kind, err)
		}
		isSyntheticNotion := kind == surface.KindNotion && (session.ID == "new" || strings.HasPrefix(session.ID, "new:"))
		if a.registry() != nil && live && !isSyntheticNotion {
			if err := a.registry().RegisterSession(*session); err != nil {
				return nil, nil, fmt.Errorf("register %s session: %w", kind, err)
			}
		}
		return session, adapter, nil
	}
	subagentPath := strings.HasPrefix(target, "@") && strings.Contains(target, "/")
	named := strings.HasPrefix(target, "@") && !subagentPath && a.registry() != nil
	if !subagentPath {
		target = strings.TrimPrefix(target, "@")
	}
	if subagentPath && a.registry() == nil {
		return nil, nil, fmt.Errorf("subagent target %q needs the session registry", target)
	}
	if a.registry() != nil {
		resolve := a.registry().ResolveTarget
		if named {
			resolve = func(name string) (string, error) { return a.registry().ExactTarget("", name) }
		}
		sid, err := resolve(target)
		if err != nil && subagentPath {
			return nil, nil, err
		}
		if err == nil {
			registered, lookupErr := a.registry().Session(sid)
			if lookupErr == nil {
				adapter, adapterErr := a.adapters().For(registered)
				if adapterErr != nil {
					return nil, nil, adapterErr
				}
				session, live, resolveErr := a.resolveSession(ctx, registered.Surface, adapter, sid, target)
				var exitedName exitedNamedSession
				if errors.As(resolveErr, &exitedName) {
					return nil, nil, resolveErr
				}
				if resolveErr != nil {
					return nil, nil, fmt.Errorf("resolve registered %s session: %w", adapter.Name(), resolveErr)
				}
				if live {
					if err := a.registry().RegisterSession(*session); err != nil {
						return nil, nil, fmt.Errorf("register %s session: %w", adapter.Name(), err)
					}
				}
				return session, adapter, nil
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, err
		}
	}
	if named {
		return nil, nil, targetNotFoundError{target: "@" + target}
	}
	type resolvedMatch struct {
		session *surface.Session
		adapter surface.Surface
	}
	var matches []resolvedMatch
	var bridgeErrors []string
	for _, s := range a.allSurfaces() {
		sess, err := s.Resolve(ctx, target)
		if err == nil {
			matches = append(matches, resolvedMatch{session: sess, adapter: s})
			continue
		}
		if strings.Contains(err.Error(), "connect Codex Desktop") ||
			strings.Contains(err.Error(), "sidecar") ||
			strings.Contains(err.Error(), "no local transcript") {
			bridgeErrors = append(bridgeErrors, fmt.Sprintf("[%s] %s", s.Name(), err))
		}
	}
	if len(matches) > 1 {
		labels := make([]string, 0, len(matches))
		for _, candidate := range matches {
			labels = append(labels, fmt.Sprintf("%s:%s", candidate.adapter.Name(), candidate.session.ID))
		}
		return nil, nil, fmt.Errorf("ambiguous target %q matched %s; qualify it as surface:target", target, strings.Join(labels, ", "))
	}
	if len(matches) == 1 {
		candidate := matches[0]
		if a.registry() != nil {
			if err := a.registry().RegisterSession(*candidate.session); err != nil {
				return nil, nil, fmt.Errorf("register %s session: %w", candidate.adapter.Name(), err)
			}
		}
		return candidate.session, candidate.adapter, nil
	}
	if len(bridgeErrors) > 0 {
		return nil, nil, fmt.Errorf("%s\nno session matched '%s' (bridge may be down)", strings.Join(bridgeErrors, "; "), target)
	}
	return nil, nil, targetNotFoundError{target: target}
}

func (a *App) ensureWritableTarget(ctx context.Context, session *surface.Session, adapter surface.Surface) error {
	if err := surface.EnsureWritableSession(ctx, adapter, session); err != nil {
		return err
	}
	isSyntheticNotion := session.Surface == surface.KindNotion && (session.ID == "new" || strings.HasPrefix(session.ID, "new:"))
	if a.registry() != nil && !isSyntheticNotion {
		if err := a.registry().RegisterSession(*session); err != nil {
			return fmt.Errorf("register %s session: %w", adapter.Name(), err)
		}
	}
	return nil
}

func (a *App) prepareDaemonSessionStream(ctx context.Context, adapter surface.Surface, session *surface.Session) (sessionstream.Subscription, error) {
	page, err := a.readSessionPage(ctx, adapter, session, surface.SessionReadRequest{Limit: 1})
	if err != nil {
		return sessionstream.Subscription{}, fmt.Errorf("capture active daemon stream cursor: %w", err)
	}
	if page.UnavailableReason != "" {
		return sessionstream.Subscription{}, fmt.Errorf("active daemon session source unavailable: %s", page.UnavailableReason)
	}
	var reader sessionStreamReader
	if a.daemonSessionStreamReader != nil {
		reader, err = a.daemonSessionStreamReader()
	} else {
		reader, err = newDaemonSessionStreamClient()
	}
	if err != nil {
		return sessionstream.Subscription{}, fmt.Errorf("open active daemon session stream: %w", err)
	}
	if reader == nil {
		return sessionstream.Subscription{}, errors.New("open active daemon session stream: reader is unavailable")
	}
	return reader.OpenSessionStream(ctx, session, page.JournalSeq)
}

func streamEventDelta(event sessionstream.Event, bodies map[string]string, versions map[string]uint64) string {
	if event.ItemID == "" {
		return event.Body
	}
	if prior, found := versions[event.ItemID]; found && ((event.Version > 0 && prior > 0 && event.Version <= prior) || (event.Version == 0 && bodies[event.ItemID] == event.Body)) {
		return ""
	}
	versions[event.ItemID] = event.Version
	previous := bodies[event.ItemID]
	bodies[event.ItemID] = event.Body
	if strings.HasPrefix(event.Body, previous) {
		return strings.TrimPrefix(event.Body, previous)
	}
	return event.Body
}

func directStreamEventDelta(event surface.StreamEvent, bodies map[string]string, versions map[string]uint64) string {
	key := event.ID
	if key == "" {
		key = event.ProviderKey
	}
	if key == "" {
		return event.Text
	}
	if prior, found := versions[key]; found && event.Version > 0 && prior > 0 && event.Version <= prior {
		return ""
	}
	versions[key] = event.Version
	previous := bodies[key]
	if event.Operation == "append" {
		bodies[key] = previous + event.Text
		return event.Text
	}
	bodies[key] = event.Text
	return surface.StreamEventDelta(event, previous)
}

func printSessionStreamEvent(event sessionstream.Event, bodies map[string]string, versions map[string]uint64) {
	if event.Kind == "tool_use" || event.Kind == "toolCall" || event.Kind == "tool_call" {
		if text := streamEventDelta(event, bodies, versions); text != "" {
			fmt.Printf("  -> %s\n", text)
		}
		return
	}
	if event.Kind == "tool_result" || event.Kind == "toolResult" {
		if text := streamEventDelta(event, bodies, versions); text != "" {
			fmt.Printf("  <- %s\n", text)
		}
		return
	}
	if !event.SpokenAssistantContent() {
		return
	}
	if text := streamEventDelta(event, bodies, versions); text != "" {
		fmt.Print(text)
	}
}

func directStreamTerminalError(event surface.StreamEvent, turnID string) error {
	if !event.Failed() {
		return nil
	}
	return fmt.Errorf("turn %s did not complete successfully: %s", turnID, event.FailureReason())
}

func streamDirectOutput(ctx context.Context, surf surface.Surface, sess *surface.Session, turnID string, timeout time.Duration) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	bodies := map[string]string{}
	versions := map[string]uint64{}
	var terminalErr error
	err := surf.Stream(streamCtx, sess, turnID, func(event surface.StreamEvent) {
		switch event.Class() {
		case surface.StreamEventMessage:
			if event.Role != "" && event.Role != "assistant" {
				return
			}
			if text := directStreamEventDelta(event, bodies, versions); text != "" {
				fmt.Print(text)
			}
		case surface.StreamEventToolCall:
			if text := event.Text; text != "" {
				fmt.Printf("  -> %s\n", text)
			}
		case surface.StreamEventToolResult:
			if text := event.Text; text != "" {
				fmt.Printf("  <- %s\n", text)
			}
		case surface.StreamEventTerminal:
			if err := directStreamTerminalError(event, turnID); err != nil {
				terminalErr = err
				cancel()
				return
			}
			fmt.Println()
		}
	}, timeout)
	if terminalErr != nil {
		return terminalErr
	}
	return err
}

func consumeSessionReply(ctx context.Context, subscription sessionstream.Subscription, turnID string) (*surface.ReplyResult, error) {
	bodies := map[string]string{}
	versions := map[string]uint64{}
	var latest string
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case event, ok := <-subscription.Events:
			if !ok {
				return nil, fmt.Errorf("session stream ended before turn %s completed", turnID)
			}
			if event.Kind == "source-error" {
				return nil, fmt.Errorf("session source failed: %s", event.Reason)
			}
			if event.TurnID != turnID {
				continue
			}
			if event.SpokenAssistantContent() {
				if prior := bodies[event.ItemID]; event.Version == 0 || event.Version > versions[event.ItemID] {
					if event.ItemID != "" && strings.HasPrefix(event.Body, prior) {
						bodies[event.ItemID] = event.Body
					} else if event.ItemID != "" {
						bodies[event.ItemID] = event.Body
					}
					versions[event.ItemID] = event.Version
					latest = event.Body
				}
			}
			if event.Terminal() {
				if event.Failed() {
					return nil, fmt.Errorf("turn %s did not complete successfully: %s", turnID, event.FailureReason())
				}
				return &surface.ReplyResult{Text: latest, Done: true, Source: "daemon-journal"}, nil
			}
		}
	}
}

func consumeDaemonStreamOutput(ctx context.Context, subscription sessionstream.Subscription, turnID string, timeout time.Duration) error {
	bodies := map[string]string{}
	versions := map[string]uint64{}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for stream after %s: %w", timeout, ctx.Err())
		case event, ok := <-subscription.Events:
			if !ok {
				return fmt.Errorf("session stream ended before turn %s completed", turnID)
			}
			if event.Kind == "source-error" {
				return fmt.Errorf("session source failed: %s", event.Reason)
			}
			if event.TurnID != turnID {
				continue
			}
			if event.Kind == "done" {
				if event.Failed() {
					return fmt.Errorf("turn %s did not complete successfully: %s", turnID, event.FailureReason())
				}
				fmt.Println()
				return nil
			}
			printSessionStreamEvent(event, bodies, versions)
		}
	}
}
