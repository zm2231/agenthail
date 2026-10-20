package broadcast

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type Target struct {
	Name      string `json:"name"`
	Channel   string `json:"channel,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	Project   string `json:"project,omitempty"`
}

func (t Target) Label() string {
	if t.Project != "" {
		return t.Project
	}
	return t.Name
}

type project struct {
	ID          string `json:"id"`
	LocalID     string `json:"localId"`
	DisplayName string `json:"displayName"`
}

func projects(reg *registry.Registry) (map[string]project, error) {
	found := map[string]project{}
	snapshot, err := reg.CatalogSnapshot()
	if err != nil {
		return nil, err
	}
	for _, record := range snapshot.Sessions {
		if item, ok := sessionProject(record); ok {
			found[item.ID] = item
		}
	}
	records, err := reg.ListProjects()
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if !record.Added {
			continue
		}
		id := record.ID
		if record.Key != "" {
			id = record.Key
		}
		if _, ok := found[id]; !ok {
			found[id] = project{ID: id, LocalID: record.ID, DisplayName: record.Title}
		}
	}
	coordinators, err := reg.Coordinators()
	if err != nil {
		return nil, err
	}
	for _, spec := range coordinators {
		if _, ok := found[spec.ProjectID]; !ok {
			found[spec.ProjectID] = project{ID: spec.ProjectID, DisplayName: filepath.Base(spec.ProjectRoot)}
		}
	}
	return found, nil
}

func sessionProject(record registry.CatalogSessionState) (project, bool) {
	var item project
	if len(record.Project) == 0 || json.Unmarshal(record.Project, &item) != nil || item.ID == "" {
		return project{}, false
	}
	return item, true
}

func ValidateChannelName(reg *registry.Registry, name string) error {
	if name == "" || len(name) > 80 || strings.ContainsAny(name, " \t\r\n/:#") {
		return fmt.Errorf("a channel name must be 1 to 80 characters without spaces, /, :, or #")
	}
	known, err := projects(reg)
	if err != nil {
		return err
	}
	for _, item := range known {
		if item.ID == name || item.DisplayName == name {
			return fmt.Errorf("#%s already names project %s (%s); choose another channel name", name, item.DisplayName, item.ID)
		}
	}
	return nil
}

// A name that is both a stored channel and a project, or that several
// projects share, is refused rather than guessed.
func Resolve(reg *registry.Registry, name string) (Target, error) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "#")
	if name == "" {
		return Target{}, fmt.Errorf("a channel name is required")
	}
	stored, err := reg.ChannelExists(name)
	if err != nil {
		return Target{}, err
	}
	known, err := projects(reg)
	if err != nil {
		return Target{}, err
	}
	var matches []project
	if item, ok := known[name]; ok {
		matches = append(matches, item)
	} else {
		for _, item := range known {
			if item.DisplayName == name {
				matches = append(matches, item)
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	switch {
	case stored && len(matches) > 0:
		return Target{}, fmt.Errorf("#%s is both a channel and project %s; rename the channel with 'agenthail channel rename %s <new-name>', or address the project by its id #%s", name, describe(matches), name, matches[0].ID)
	case stored:
		return Target{Name: name, Channel: name}, nil
	case len(matches) > 1:
		return Target{}, fmt.Errorf("#%s names several projects: %s; address one by its id", name, describe(matches))
	case len(matches) == 1:
		return Target{Name: name, ProjectID: matches[0].ID, Project: matches[0].DisplayName}, nil
	}
	return Target{}, fmt.Errorf("no channel or project is named #%s", name)
}

func describe(items []project) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("%s (#%s)", item.DisplayName, item.ID))
	}
	return strings.Join(parts, ", ")
}

type Recipient struct {
	SessionID   string `json:"sessionId,omitempty"`
	Coordinator string `json:"coordinator,omitempty"`
	HostID      string `json:"hostId,omitempty"`
	Outcome     string `json:"outcome"`
	Detail      string `json:"detail,omitempty"`
}

const (
	OutcomeSent      = "sent"
	OutcomeQueued    = "queued"
	OutcomeSubmitted = "submitted"
	OutcomeFailed    = "failed"
)

type Result struct {
	Target     Target      `json:"target"`
	Recipients []Recipient `json:"recipients"`
}

func (r Result) Count(outcome string) int {
	count := 0
	for _, recipient := range r.Recipients {
		if recipient.Outcome == outcome {
			count++
		}
	}
	return count
}

type Delivery interface {
	Session(ctx context.Context, sessionID, message, sourceSessionID string) (*delivery.Receipt, error)
	PeerCoordinator(ctx context.Context, spec registry.CoordinatorSpec, message, sourceSessionID string) (*delivery.Receipt, error)
}

type Broadcaster struct {
	Registry     *registry.Registry
	Coordinators coordinator.Service
	Delivery     Delivery
}

func (b Broadcaster) Send(ctx context.Context, target Target, message, label, sourceSessionID string) (Result, error) {
	result := Result{Target: target}
	if sourceSessionID != "" {
		sourceSessionID = b.Coordinators.Canonical(sourceSessionID)
	}
	payload := fmt.Sprintf("[from %s via #%s] %s", label, target.Label(), message)
	members, coordinators, err := b.members(target, time.Now())
	if err != nil {
		return result, err
	}
	members = slices.DeleteFunc(members, func(member string) bool { return sourceSessionID != "" && member == sourceSessionID })
	coordinators = slices.DeleteFunc(coordinators, func(spec registry.CoordinatorSpec) bool {
		return sourceSessionID != "" && spec.SessionID == sourceSessionID
	})
	if len(members) == 0 && len(coordinators) == 0 {
		return result, fmt.Errorf("#%s has no members other than the sender", target.Name)
	}
	for _, spec := range coordinators {
		result.Recipients = append(result.Recipients, b.toCoordinator(ctx, target, spec, payload, sourceSessionID))
	}
	for _, member := range members {
		result.Recipients = append(result.Recipients, b.toSession(ctx, target, member, payload, sourceSessionID))
	}
	return result, nil
}

// A project's coordinators are reached even when idle past the active
// window, since a send starts or resumes one anyway.
func (b Broadcaster) members(target Target, now time.Time) ([]string, []registry.CoordinatorSpec, error) {
	if target.ProjectID == "" {
		members, err := b.Registry.ChannelMembers(target.Channel)
		return members, nil, err
	}
	coordinators, err := b.Registry.ProjectCoordinators(target.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	coordinated := map[string]bool{}
	for _, spec := range coordinators {
		if spec.SessionID == "" {
			continue
		}
		rawID := spec.SessionID
		if spec.HostID != "" {
			_, rawID, _ = hosts.Split(spec.SessionID)
		}
		coordinated[spec.HostID+"\x00"+rawID] = true
	}
	snapshot, err := b.Registry.CatalogSnapshot()
	if err != nil {
		return nil, nil, err
	}
	archive, err := b.Registry.ArchiveState()
	if err != nil {
		return nil, nil, err
	}
	var members []string
	for _, record := range snapshot.Sessions {
		item, ok := sessionProject(record)
		if !ok || item.ID != target.ProjectID || record.Freshness.Stale || coordinated[record.HostID+"\x00"+record.Session.ID] {
			continue
		}
		if !surface.RecentlyActive(record.Session, now) {
			continue
		}
		if archived(record, item, archive) {
			continue
		}
		members = append(members, record.Session.ID)
	}
	sort.Strings(members)
	return members, coordinators, nil
}

// A paired host's row carries that host's archive flags in its projection.
func archived(record registry.CatalogSessionState, item project, archive registry.ArchiveState) bool {
	if record.HostID != "" {
		var mirrored struct {
			Archived bool `json:"archived"`
		}
		return json.Unmarshal([]byte(record.ProjectionFingerprint), &mirrored) == nil && mirrored.Archived
	}
	if _, found := archive.Sessions[record.Session.ID]; found {
		return true
	}
	_, found := archive.Projects[item.LocalID]
	return found
}

func (b Broadcaster) toCoordinator(ctx context.Context, target Target, spec registry.CoordinatorSpec, payload, source string) Recipient {
	recipient := Recipient{Coordinator: "@" + spec.Name, HostID: spec.HostID, SessionID: spec.SessionID}
	if spec.HostID == "" {
		outcome, err := b.Coordinators.Deliver(ctx, spec, payload, surface.SendOptions{SourceSessionID: source})
		if err != nil {
			return failed(recipient, err)
		}
		if outcome.Session != nil {
			recipient.SessionID = outcome.Session.ID
		}
		return received(recipient, outcome.Receipt)
	}
	recipient.Coordinator = spec.HostID + "::@" + spec.Name
	if _, rawID, qualified := hosts.Split(spec.SessionID); qualified {
		if owner, err := b.Registry.SessionHost(rawID); err == nil && owner == spec.HostID {
			return b.queueForHost(target, recipient, rawID, payload, source)
		}
	}
	receipt, err := b.Delivery.PeerCoordinator(ctx, spec, payload, source)
	if err != nil {
		return failed(recipient, err)
	}
	return received(recipient, receipt)
}

func (b Broadcaster) toSession(ctx context.Context, target Target, sessionID, payload, source string) Recipient {
	recipient := Recipient{SessionID: sessionID}
	owner, err := b.Registry.SessionHost(sessionID)
	if err != nil {
		return failed(recipient, err)
	}
	if owner != "" {
		recipient.HostID, recipient.SessionID = owner, hosts.Qualify(owner, sessionID)
		return b.queueForHost(target, recipient, sessionID, payload, source)
	}
	receipt, err := b.Delivery.Session(ctx, sessionID, payload, source)
	if err != nil {
		return failed(recipient, err)
	}
	return received(recipient, receipt)
}

func (b Broadcaster) queueForHost(target Target, recipient Recipient, sessionID, payload, source string) Recipient {
	key := "channel:" + target.Name + ":" + sessionID + ":" + uuid.NewString()
	if _, err := b.Registry.QueueHostSend(registry.HostSend{SessionID: sessionID, Message: payload, Key: key, SourceSessionID: source}); err != nil {
		return failed(recipient, err)
	}
	recipient.Outcome, recipient.Detail = OutcomeQueued, "held for its host"
	return recipient
}

func received(recipient Recipient, receipt *delivery.Receipt) Recipient {
	if receipt == nil {
		recipient.Outcome = OutcomeSent
		return recipient
	}
	recipient.Detail = receipt.Detail
	switch receipt.Evidence {
	case surface.EvidenceQueued:
		recipient.Outcome = OutcomeQueued
	case surface.EvidenceSubmitted:
		recipient.Outcome = OutcomeSubmitted
	default:
		recipient.Outcome = OutcomeSent
	}
	return recipient
}

func failed(recipient Recipient, err error) Recipient {
	recipient.Outcome, recipient.Detail = OutcomeFailed, err.Error()
	return recipient
}
