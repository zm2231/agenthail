package daemon

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"reflect"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type sessionStreamAppend struct {
	Stream    string `json:"stream"`
	SessionID string `json:"sessionId"`
	Seq       uint64 `json:"seq"`
	Type      string `json:"type"`
	ItemID    string `json:"itemId"`
	From      int    `json:"from"`
	Text      string `json:"text"`
	Version   uint64 `json:"version"`
	TS        string `json:"ts"`
	Final     bool   `json:"final,omitempty"`
}

type bodyViewer func(ref string, head, from int) (registry.SessionJournalBodyView, bool, error)

// heldReplyLimit bounds the replies one connection remembers. A reply still
// streaming is touched on every delta, so it is never the one let go; a reply
// let go that grows again is sent whole.
const heldReplyLimit = 64

// sessionStreamWriter belongs to one connection: it remembers each reply's
// bytes that connection was sent, so a reply that grows is sent as the bytes
// it gained. Any other change, and a row this connection has not seen, is
// sent whole.
type sessionStreamWriter struct {
	w         io.Writer
	sessionID string
	toItem    func(sessionJournalPayload) surface.TimelineItem
	view      bodyViewer
	held      map[string]*heldReply
	clock     uint64
}

type heldReply struct {
	shape   sessionJournalPayload
	bytes   int
	ref     string
	stamp   uint64
	checked bool
	digest  hash.Hash
	touched uint64
}

func newSessionStreamWriter(w io.Writer, sessionID string, toItem func(sessionJournalPayload) surface.TimelineItem, view bodyViewer) *sessionStreamWriter {
	return &sessionStreamWriter{w: w, sessionID: sessionID, toItem: toItem, view: view, held: map[string]*heldReply{}}
}

func (s *sessionStreamWriter) write(entry registry.SessionJournalEntry) error {
	var row sessionJournalPayload
	if err := json.Unmarshal(entry.Payload, &row); err != nil {
		return fmt.Errorf("invalid session journal payload")
	}
	held := s.held[row.ItemID]
	delete(s.held, row.ItemID)
	if !journalRowIsItem(row) || row.ItemID == "" {
		return s.writeWhole(entry, row, nil)
	}
	item := s.toItem(row)
	if item.Role != "assistant" || item.Agent != nil || item.Text != row.Body {
		return s.writeWhole(entry, row, &item)
	}
	if held != nil && reflect.DeepEqual(held.shape, replyShape(row)) {
		gained, stamp, ok, err := s.gained(held, row)
		if err != nil {
			return err
		}
		if ok {
			if err := s.writeAppend(entry.Seq, row, held.bytes, gained); err != nil {
				return err
			}
			held.digest.Write(gained)
			held.bytes += len(gained)
			held.ref, held.stamp, held.checked = row.BodyRef, stamp, row.BodyRef != ""
			s.keep(row.ItemID, held)
			return nil
		}
	}
	if err := s.writeWhole(entry, row, &item); err != nil {
		return err
	}
	digest := sha256.New()
	digest.Write([]byte(row.Body))
	s.keep(row.ItemID, &heldReply{shape: replyShape(row), bytes: len(row.Body), digest: digest})
	return nil
}

func (s *sessionStreamWriter) keep(itemID string, held *heldReply) {
	s.clock++
	held.touched = s.clock
	s.held[itemID] = held
	if len(s.held) <= heldReplyLimit {
		return
	}
	oldest, at := "", s.clock
	for id, candidate := range s.held {
		if candidate.touched < at {
			oldest, at = id, candidate.touched
		}
	}
	delete(s.held, oldest)
}

// gained returns the bytes the body has past what the connection holds, when
// what it holds is still the body's prefix. A stamp the connection already
// checked proves that without reading the prefix.
func (s *sessionStreamWriter) gained(held *heldReply, row sessionJournalPayload) ([]byte, uint64, bool, error) {
	if row.BodyRef == "" {
		body := []byte(row.Body)
		if len(body) < held.bytes || !sameDigest(held.digest, body[:held.bytes]) {
			return nil, 0, false, nil
		}
		return body[held.bytes:], 0, true, nil
	}
	if held.checked && held.ref == row.BodyRef {
		view, found, err := s.view(row.BodyRef, 0, held.bytes)
		if err != nil {
			return nil, 0, false, err
		}
		if found && view.Stamp == held.stamp && view.Total >= held.bytes && utf8.Valid(view.Tail) {
			return view.Tail, view.Stamp, true, nil
		}
	}
	view, found, err := s.view(row.BodyRef, held.bytes, held.bytes)
	if err != nil || !found || view.Total < held.bytes || !utf8.Valid(view.Tail) || !sameDigest(held.digest, view.Head) {
		return nil, 0, false, err
	}
	return view.Tail, view.Stamp, true, nil
}

func sameDigest(held hash.Hash, prefix []byte) bool {
	sum := sha256.Sum256(prefix)
	return bytes.Equal(held.Sum(nil), sum[:])
}

func replyShape(row sessionJournalPayload) sessionJournalPayload {
	row.Body, row.Truncated, row.BodyRef, row.Version, row.TS, row.Final, row.text = "", false, "", 0, "", false, nil
	return row
}

func (s *sessionStreamWriter) writeAppend(seq uint64, row sessionJournalPayload, from int, gained []byte) error {
	encoded, err := marshalAPIJSON(sessionStreamAppend{Stream: "session", SessionID: s.sessionID, Seq: seq, Type: "append", ItemID: row.ItemID, From: from, Text: string(gained), Version: row.Version, TS: row.TS, Final: row.Final})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.w, "id: %d\nevent: item\ndata: %s\n\n", seq, encoded)
	return err
}

func (s *sessionStreamWriter) writeWhole(entry registry.SessionJournalEntry, row sessionJournalPayload, item *surface.TimelineItem) error {
	if item == nil && journalRowIsItem(row) {
		presented := s.toItem(row)
		item = &presented
	}
	return writeSessionStreamEnvelope(s.w, sessionStreamEnvelope{Stream: "session", SessionID: s.sessionID, Seq: entry.Seq, Type: "item", Data: entry.Payload, Item: item})
}
