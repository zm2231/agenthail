package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
)

const fragmentCandidateLimit = 10

type inexactTargetError struct {
	target  string
	matches []string
}

func (e inexactTargetError) Error() string {
	var b strings.Builder
	if len(e.matches) > 1 {
		fmt.Fprintf(&b, "ambiguous target %q; nothing was delivered. It matches:", e.target)
	} else {
		fmt.Fprintf(&b, "target %q matches a session only by name or folder; nothing was delivered. It matches:", e.target)
	}
	for _, match := range e.matches {
		b.WriteString("\n  " + match)
	}
	b.WriteString("\naddress one by @alias, session id or surface:<id>")
	return b.String()
}

// resolveDeliveryTarget resolves the session a message is delivered to, or a
// relay or channel will deliver to. Only a target that identifies one session
// resolves: an alias, a session ID or unique ID prefix, a Claude conversation
// ID or PID, surface:<one of those>, or @<parent>/<nickname> under such a
// parent. A name or folder fragment is refused with the sessions it matches,
// so a mistyped word never becomes a delivery to whichever session happens to
// contain it.
func (a *App) resolveDeliveryTarget(ctx context.Context, target string) (*surface.Session, surface.Surface, *remoteTarget, error) {
	target, err := a.expandLead(target)
	if err != nil {
		return nil, nil, nil, err
	}
	session, adapter, remote, err := a.resolveAnyTarget(ctx, target)
	if err == nil {
		if a.namesExactly(target, session, remote) {
			return session, adapter, remote, nil
		}
		matches := a.labels(a.fragmentCandidates(target))
		if len(matches) == 0 {
			matches = []string{a.deliveryLabel(session, remote)}
		}
		return nil, nil, nil, inexactTargetError{target: target, matches: matches}
	}
	var exited exitedNamedSession
	if a.registry() == nil || a.registeredExactly(target) || errors.As(err, &exited) {
		return nil, nil, nil, err
	}
	candidates := a.fragmentCandidates(target)
	if len(candidates) > 1 || len(candidates) == 1 && !strings.HasPrefix(candidates[0].ID, a.localSelector(target)) {
		return nil, nil, nil, inexactTargetError{target: target, matches: a.labels(candidates)}
	}
	return nil, nil, nil, err
}

// namesExactly judges a paired host's match by that host's answer, since only
// it knows the session's PID, transcript and subagents.
func (a *App) namesExactly(target string, session *surface.Session, remote *remoteTarget) bool {
	if remote != nil {
		return remote.Exact
	}
	if _, rest, qualified := hosts.Split(target); qualified {
		target = rest
	}
	if kind, selector, ok := a.surfaceQualified(target); ok {
		if session.Surface != kind {
			return false
		}
		if session.ID == selector {
			return true
		}
		target = selector
	}
	if a.registry() == nil {
		return surface.IdentifiedBy(*session, strings.TrimPrefix(target, "@"))
	}
	return a.registry().NamesSession(*session, target)
}

func (a *App) surfaceQualified(target string) (surface.SurfaceKind, string, bool) {
	kindText, selector, ok := strings.Cut(target, ":")
	if !ok {
		return "", "", false
	}
	kind := surface.SurfaceKind(strings.ToLower(kindText))
	if a.adapters().ForSurface(kind) == nil {
		return "", "", false
	}
	return kind, selector, true
}

func (a *App) registeredExactly(target string) bool {
	if _, rest, qualified := hosts.Split(target); qualified {
		target = rest
	}
	kind := surface.SurfaceKind("")
	if qualifiedKind, selector, ok := a.surfaceQualified(target); ok {
		kind, target = qualifiedKind, selector
	}
	_, err := a.registry().ExactTarget(kind, strings.TrimPrefix(target, "@"))
	return err == nil
}

// fragmentCandidates lists the local sessions a fragment matches. A
// host-qualified target's matches belong to that host and come from its
// resolve answer instead.
func (a *App) fragmentCandidates(target string) []*surface.Session {
	if a.registry() == nil {
		return nil
	}
	if _, _, qualified := hosts.Split(target); qualified {
		return nil
	}
	kind := surface.SurfaceKind("")
	if qualifiedKind, _, ok := a.surfaceQualified(target); ok {
		kind = qualifiedKind
	}
	selector := a.localSelector(target)
	if strings.HasPrefix(selector, "@") {
		return nil
	}
	ids, err := a.registry().FragmentMatches(kind, selector, fragmentCandidateLimit)
	if err != nil {
		return nil
	}
	sessions := make([]*surface.Session, 0, len(ids))
	for _, id := range ids {
		if session, lookupErr := a.registry().Session(id); lookupErr == nil {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

func (a *App) localSelector(target string) string {
	if _, selector, ok := a.surfaceQualified(target); ok {
		return selector
	}
	return target
}

func (a *App) labels(sessions []*surface.Session) []string {
	labels := make([]string, 0, len(sessions))
	for _, session := range sessions {
		labels = append(labels, a.deliveryLabel(session, nil))
	}
	return labels
}

func (a *App) deliveryLabel(session *surface.Session, remote *remoteTarget) string {
	if remote != nil {
		label := remote.Host.Name + hosts.Separator + remote.ID
		if remote.Alias != "" {
			label += "  " + remote.Host.Name + hosts.Separator + "@" + remote.Alias
		}
		if remote.Name != "" {
			label += "  " + remote.Name
		}
		return label
	}
	label := string(session.Surface) + ":" + session.ID
	if a.registry() != nil {
		if alias, err := a.registry().ReverseAlias(session.ID); err == nil && alias != "" {
			label += "  @" + alias
		}
	}
	if session.Name != "" {
		label += "  " + session.Name
	}
	return label
}
