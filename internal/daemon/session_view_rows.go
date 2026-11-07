package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type presentationFingerprint struct {
	generation uint64
	row        dashboardSession
	raw        json.RawMessage
}

type presentationMergedFields struct {
	WaitingOn       json.RawMessage `json:"waitingOn"`
	BackgroundTasks json.RawMessage `json:"backgroundTasks"`
	UsageLimit      json.RawMessage `json:"usageLimit"`
	TurnStop        json.RawMessage `json:"turnStop"`
}

// Every projection write takes a new generation, so an unchanged generation means an unchanged projection.
type presentationFingerprints struct {
	epoch string
	rows  map[string]presentationFingerprint
}

func (c *presentationFingerprints) stale(id string, generation uint64) bool {
	cached, ok := c.rows[id]
	return !ok || cached.generation != generation
}

func (c *presentationFingerprints) valid(read registry.CatalogPresentation) bool {
	if c.rows == nil || c.epoch != read.HostEpoch {
		return false
	}
	for _, row := range read.Rows {
		if cached, ok := c.rows[row.ID]; ok && row.Freshness.Generation < cached.generation {
			return false
		}
	}
	return true
}

func (c *presentationFingerprints) reset(epoch string) {
	c.epoch = epoch
	c.rows = map[string]presentationFingerprint{}
}

func (c *presentationFingerprints) adopt(read registry.CatalogPresentation) error {
	live := make(map[string]bool, len(read.Rows))
	for _, row := range read.Rows {
		live[row.ID] = true
		raw, ok := read.Fingerprints[row.ID]
		if !ok {
			continue
		}
		var decoded presentationFingerprint
		if err := json.Unmarshal(raw, &decoded.row); err != nil {
			return fmt.Errorf("read presentation %s: %w", row.ID, err)
		}
		decoded.raw = raw
		decoded.generation = row.Freshness.Generation
		c.rows[row.ID] = decoded
	}
	if !read.History {
		return nil
	}
	for id := range c.rows {
		if !live[id] {
			delete(c.rows, id)
		}
	}
	return nil
}

func presentationRow(record registry.CatalogPresentationRow, source presentationFingerprint, prViews func([]registry.SessionPR) []prView) (dashboardSession, error) {
	row := source.row
	row.Holders, row.Plan, row.Runtime, row.ClaudeConfigRoot, row.ConfiguredModel = nil, nil, nil, nil, ""
	row.Name, row.Surface, row.Status, row.Cwd = record.Name, surface.SurfaceKind(record.Surface), surface.SessionStatus(record.Status), record.Cwd
	row.LastActive, row.RuntimeKind, row.Source, row.Transport = record.LastActive, surface.RuntimeKind(record.RuntimeKind), record.Source, record.Transport
	row.UnavailableReason, row.QueueCount, row.RegisteredAt = record.UnavailableReason, record.QueueCount, record.RegisteredAt
	if record.Subagent == nil {
		row.Subagent = nil
	} else {
		subagent := surface.Subagent{}
		if row.Subagent != nil {
			subagent = *row.Subagent
		}
		subagent.ParentID, subagent.RootID, subagent.Depth, subagent.Nickname, subagent.Role = record.Subagent.ParentID, record.Subagent.RootID, record.Subagent.Depth, record.Subagent.Nickname, record.Subagent.Role
		row.Subagent = &subagent
	}
	row.WaitingOn, row.BackgroundTasks, row.UsageLimit, row.TurnStop = nil, nil, nil, nil
	if len(record.WaitingOn)+len(record.BackgroundTasks)+len(record.UsageLimit)+len(record.TurnStop) > 0 {
		var projected presentationMergedFields
		if err := json.Unmarshal(source.raw, &projected); err != nil {
			return row, err
		}
		var err error
		if row.WaitingOn, err = mergedPresentationField[surface.Waiting](projected.WaitingOn, record.WaitingOn); err != nil {
			return row, err
		}
		if row.BackgroundTasks, err = mergedPresentationField[surface.BackgroundTasks](projected.BackgroundTasks, record.BackgroundTasks); err != nil {
			return row, err
		}
		if row.UsageLimit, err = mergedPresentationField[surface.UsageLimit](projected.UsageLimit, record.UsageLimit); err != nil {
			return row, err
		}
		if row.TurnStop, err = mergedPresentationField[surface.TurnStop](projected.TurnStop, record.TurnStop); err != nil {
			return row, err
		}
	}
	switch {
	case record.Alias.Valid:
		row.Alias = record.Alias.String
	case record.HostID == "":
		row.Alias = ""
	}
	if record.HostID == "" {
		row.Archived = record.Archived
		row.PinnedAt, row.KeepAlive = record.PinnedAt, keepAliveRow(record.KeepAlive)
		row.Snooze = snoozeRow(record.Snooze)
		row.EndTurn = endTurnRow(record.TurnEnd)
		row.Worktree = workRow(record.Work)
		row.PRs = nil
		if len(record.PRs) > 0 {
			row.PRs = prViews(record.PRs)
		}
	}
	return row, nil
}

func mergedPresentationField[T any](projected, column json.RawMessage) (*T, error) {
	if len(column) == 0 {
		return nil, nil
	}
	merged, err := jsonMergePatch(projected, column)
	if err != nil || merged == nil {
		return nil, err
	}
	var value T
	if err := json.Unmarshal(merged, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func jsonMergePatch(target, patch json.RawMessage) (json.RawMessage, error) {
	var patchValue, targetValue any
	if err := decodeJSONNumber(patch, &patchValue); err != nil {
		return nil, err
	}
	if len(target) > 0 {
		if err := decodeJSONNumber(target, &targetValue); err != nil {
			return nil, err
		}
	}
	merged := mergePatchValue(targetValue, patchValue)
	if merged == nil {
		return nil, nil
	}
	return json.Marshal(merged)
}

func decodeJSONNumber(raw json.RawMessage, value *any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(value)
}

func mergePatchValue(target, patch any) any {
	patchObject, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	targetObject, ok := target.(map[string]any)
	if !ok {
		targetObject = map[string]any{}
	}
	for key, value := range patchObject {
		if value == nil {
			delete(targetObject, key)
			continue
		}
		targetObject[key] = mergePatchValue(targetObject[key], value)
	}
	return targetObject
}
