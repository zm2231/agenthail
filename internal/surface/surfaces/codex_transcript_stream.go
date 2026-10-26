package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) streamTranscript(ctx context.Context, session *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	path := codexTranscriptPath(session)
	if path == "" {
		return fmt.Errorf("Codex local transcript is unavailable")
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("Codex local transcript is unavailable: %w", surface.ErrTranscriptUnavailable)
	}
	identity := transcriptFileIdentity(fileInfo)
	if identity == "" {
		return fmt.Errorf("Codex transcript identity is unavailable: %w", surface.ErrTranscriptUnavailable)
	}
	if session.TranscriptIdentity != "" && session.TranscriptIdentity != identity {
		session.TranscriptOffset = 0
		session.CodexCurrentTurnID = ""
		session.CodexPendingEventUser = false
		session.CodexPendingEventTurn = ""
		session.CodexToolWindow = surface.CodexToolWindow{}
	}
	session.TranscriptIdentity = identity
	offset := session.TranscriptOffset
	if fileInfo.Size() < offset {
		offset, session.TranscriptOffset = 0, 0
		session.CodexCurrentTurnID = ""
		session.CodexPendingEventUser = false
		session.CodexPendingEventTurn = ""
		session.CodexToolWindow = surface.CodexToolWindow{}
	}
	meter, err := codexStreamTurnMeter(ctx, path, offset)
	if err != nil {
		return err
	}
	wake := filewake.Follow(path)
	defer wake.Release()
	deadline := time.Now().Add(timeout)
	currentTurnID := session.CodexCurrentTurnID
	for first := true; first || time.Now().Before(deadline); first = false {
		currentInfo, statErr := os.Stat(path)
		if statErr != nil || currentInfo == nil {
			return fmt.Errorf("Codex transcript changed during stream: %w", surface.ErrTranscriptUnavailable)
		}
		if currentIdentity := transcriptFileIdentity(currentInfo); currentIdentity != identity || currentInfo.Size() < offset {
			identity, offset = currentIdentity, 0
			session.TranscriptIdentity, session.TranscriptOffset = identity, 0
			currentTurnID = ""
			session.CodexCurrentTurnID = ""
			session.CodexPendingEventUser = false
			session.CodexPendingEventTurn = ""
			session.CodexToolWindow = surface.CodexToolWindow{}
			meter, err = codexStreamTurnMeter(ctx, path, 0)
			if err != nil {
				return err
			}
		}
		lineOffset := offset
		next, err := scanAppendedJSONL(ctx, path, offset, maxCodexTranscriptRecordBytes, func(line []byte) error {
			recordOffset := lineOffset
			lineOffset += int64(len(line))
			var record map[string]any
			if json.Unmarshal(line, &record) != nil {
				return nil
			}
			if turnID := codexRecordTurnID(record); turnID != "" {
				currentTurnID = turnID
			}
			turn := meter.observe(record)
			items := codexRecordItems(&session.CodexToolWindow, record, currentTurnID)
			effectiveTurnID := currentTurnID
			for index := range items {
				if effectiveTurnID != "" {
					items[index].TurnID = effectiveTurnID
				}
				if items[index].Kind == "done" {
					items[index].Turn = turn
				}
			}
			projection := codexUserProjection{pending: session.CodexPendingEventUser, turnID: session.CodexPendingEventTurn}
			if projection.consume(record, items, effectiveTurnID) {
				session.CodexPendingEventUser = projection.pending
				session.CodexPendingEventTurn = projection.turnID
				session.CodexCurrentTurnID = currentTurnID
				return nil
			}
			session.CodexPendingEventUser = projection.pending
			session.CodexPendingEventTurn = projection.turnID
			session.CodexCurrentTurnID = currentTurnID
			if len(items) == 0 {
				return nil
			}
			if err := decorateTimelineAttachments(ctx, items, record, "codex", recordOffset); err != nil {
				return err
			}
			for index, item := range items {
				emitCodexTranscriptItem(session, uuid, record, line, recordOffset, index, item, currentTurnID, onEvent)
			}
			return nil
		})
		if err != nil {
			return err
		}
		offset = next
		session.TranscriptOffset = offset
		session.TranscriptOffsetSet = true
		if err := awaitTranscript(ctx, wake); err != nil {
			return err
		}
	}
	return fmt.Errorf("stream timed out after %s: %w", timeout, surface.ErrStreamWindow)
}

func emitCodexTranscriptItem(session *surface.Session, uuid string, record map[string]any, line []byte, offset int64, index int, item surface.TimelineItem, currentTurnID string, onEvent func(surface.StreamEvent)) {
	turnID := codexRecordTurnID(record)
	if turnID == "" {
		turnID = currentTurnID
	}
	if uuid != "" && turnID != "" && turnID != uuid {
		return
	}
	key := codexServedItemKey(record, line, offset, item, index)
	at, _ := time.Parse(time.RFC3339Nano, str(record, "timestamp"))
	operation := "upsert"
	if item.Kind == "done" {
		operation = "phase"
	}
	onEvent(surface.StreamEvent{ID: key, ProviderKey: key, Version: uint64(len(item.Text)), Operation: operation, Final: true, TurnID: turnID, Role: item.Role, Origin: item.Origin, Sender: item.Sender, Title: item.Title, CallID: item.CallID, Status: item.Status, ResetsAt: item.ResetsAt, Attachment: item.Attachment, Files: item.Files, Truncated: item.Truncated, TruncationReason: item.TruncationReason, Timestamp: at, Kind: item.Kind, Text: item.Text, Turn: item.Turn})
}
