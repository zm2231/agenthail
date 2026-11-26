package acpsurface

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/acphost"
	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

const JournalDerivation = 1

func (a *ACP) JournalDerivation() int { return JournalDerivation }

func (a *ACP) RequiresLocalTranscript(*surface.Session) bool { return true }

func (a *ACP) TranscriptOnDisk(session *surface.Session) bool {
	_, err := os.Stat(a.path(session))
	return err == nil
}

func (a *ACP) path(session *surface.Session) string {
	return a.store.TranscriptPath(a.agent.ID, session.ID)
}

type acpTranscript struct{}

func lineItem(line []byte) (*surface.TimelineItem, bool) {
	var record acphost.Line
	if json.Unmarshal(line, &record) != nil || record.Item == nil {
		return nil, false
	}
	return record.Item, true
}

func (acpTranscript) TurnStarts() *transcript.TurnStartSearch {
	return transcript.NewTurnStartSearch(
		func(line []byte) bool { return bytes.Contains(line, []byte(`"role":"user"`)) },
		func(line []byte, record *map[string]any) error { return json.Unmarshal(line, record) },
		func(record map[string]any) bool {
			item, _ := record["item"].(map[string]any)
			return transcript.String(item, "role") == "user" && transcript.String(item, "kind") == "message"
		})
}

func (acpTranscript) RecordItems(record map[string]any) []surface.TimelineItem {
	raw, err := json.Marshal(record)
	if err != nil {
		return nil
	}
	if item, ok := lineItem(raw); ok {
		return []surface.TimelineItem{*item}
	}
	return nil
}

func (acpTranscript) AttachmentReferences(map[string]any) []transcript.AttachmentReference {
	return nil
}

func (acpTranscript) TrailingAttachment(context.Context, string, map[string]any, int64, int) (transcript.AttachmentReference, bool, error) {
	return transcript.AttachmentReference{}, false, nil
}

type acpSpan struct {
	first  map[string]int
	latest map[string]surface.TimelineItem
	byLine map[int][]string
}

func (acpTranscript) ReadSpan(_ context.Context, _ *os.File, span transcript.Span) (transcript.SpanItems, error) {
	s := &acpSpan{first: map[string]int{}, latest: map[string]surface.TimelineItem{}, byLine: map[int][]string{}}
	for index, line := range span.Lines {
		item, ok := lineItem(line)
		if !ok {
			continue
		}
		if _, seen := s.first[item.ID]; !seen {
			s.first[item.ID] = index
			s.byLine[index] = append(s.byLine[index], item.ID)
		}
		s.latest[item.ID] = *item
	}
	return s, nil
}

func (s *acpSpan) ExactFrom() int64 { return 0 }

func (s *acpSpan) Items(_ context.Context, index int, _ map[string]any, _ int64, _ []byte) ([]surface.TimelineItem, bool, error) {
	ids := s.byLine[index]
	if len(ids) == 0 {
		return nil, true, nil
	}
	items := make([]surface.TimelineItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, s.latest[id])
	}
	return items, false, nil
}

func (s *acpSpan) Reply([]transcript.Record, *surface.SessionReadResult) bool { return false }

func (a *ACP) ReadSession(ctx context.Context, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	path := a.path(session)
	read, err := transcript.ReadPage(ctx, path, acpTranscript{}, request.Before, request.Limit)
	if err != nil {
		return nil, err
	}
	if read.UnavailableReason == "" {
		identity, err := acphost.TranscriptIdentity(path)
		if err != nil {
			return nil, err
		}
		read.TranscriptIdentity = identity
	}
	read.Source = "local-transcript"
	return surface.BoundSessionRead(session, read), nil
}

func (a *ACP) TranscriptItems(ctx context.Context, session *surface.Session, request surface.TranscriptItemsRequest) (*surface.TranscriptItemsResult, error) {
	path := a.path(session)
	identity, err := acphost.TranscriptIdentity(path)
	if request.Identity != "" && (errors.Is(err, fs.ErrNotExist) || err == nil && identity != request.Identity) {
		return nil, surface.ErrTranscriptChanged
	}
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	end, err := transcript.CompleteJSONLEnd(ctx, file, info.Size())
	if err != nil {
		return nil, err
	}
	from := min(max(0, request.From), end)
	if request.MaxBytes > 0 && end-from > request.MaxBytes {
		return nil, surface.ErrTranscriptBacklog
	}
	order := []string{}
	latest := map[string]surface.TimelineItem{}
	_, err = transcript.ScanAppendedJSONL(ctx, path, from, transcript.MaxRecordBytes, func(line []byte) error {
		if item, ok := lineItem(line); ok {
			if _, seen := latest[item.ID]; !seen {
				order = append(order, item.ID)
			}
			latest[item.ID] = *item
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	items := make([]surface.TimelineItem, 0, len(order))
	for _, id := range order {
		items = append(items, latest[id])
	}
	return &surface.TranscriptItemsResult{Items: items, End: end, Identity: identity}, nil
}

func (a *ACP) Stream(ctx context.Context, session *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	path := a.path(session)
	identity, err := acphost.TranscriptIdentity(path)
	if err != nil {
		return fmt.Errorf("%s transcript is unavailable: %v: %w", a.agent.DisplayName(), err, surface.ErrTranscriptUnavailable)
	}
	offset := session.TranscriptOffset
	if session.TranscriptIdentity != "" && session.TranscriptIdentity != identity {
		offset = 0
	}
	if !session.TranscriptOffsetSet && !session.TranscriptFromStart {
		if info, err := os.Stat(path); err == nil {
			offset = info.Size()
		}
	}
	session.TranscriptIdentity = identity
	wake := filewake.Follow(path)
	defer wake.Release()
	deadline := time.Now().Add(timeout)
	for first := true; first || time.Now().Before(deadline); first = false {
		lineOffset := offset
		next, err := transcript.ScanAppendedJSONL(ctx, path, offset, transcript.MaxRecordBytes, func(line []byte) error {
			recordOffset := lineOffset
			lineOffset += int64(len(line))
			item, ok := lineItem(line)
			if !ok || uuid != "" && item.TurnID != "" && item.TurnID != uuid {
				return nil
			}
			onEvent(streamEvent(*item, recordOffset))
			return nil
		})
		if err != nil {
			return err
		}
		offset = next
		session.TranscriptOffset, session.TranscriptOffsetSet = offset, true
		if err := transcript.Await(ctx, wake); err != nil {
			return err
		}
	}
	return fmt.Errorf("stream timed out after %s: %w", timeout, surface.ErrStreamWindow)
}

func streamEvent(item surface.TimelineItem, offset int64) surface.StreamEvent {
	at, _ := time.Parse(time.RFC3339Nano, item.Timestamp)
	operation := "upsert"
	if item.Kind == "done" {
		operation = "phase"
	}
	return surface.StreamEvent{ID: item.ID, ProviderKey: "timeline:" + item.ID, Version: uint64(offset + 1), Operation: operation, Final: true, TurnID: item.TurnID,
		Role: item.Role, Origin: item.Origin, Sender: item.Sender, Title: item.Title, CallID: item.CallID, Status: item.Status,
		Timestamp: at, Kind: item.Kind, Text: item.Text, MessageID: item.MessageID}
}
