package claudepeer

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// recordRejection keeps a refused frame in delivery history as a failure,
// with its message text when the frame carried one.
func recordRejection(reg *registry.Registry, c Config, f frame, raw []byte, err error) {
	message := string(raw)
	var body messageBody
	if json.Unmarshal(f.Message, &body) == nil && body.Content != "" {
		message = body.Content
	}
	_ = reg.RecordHistory(registry.HistoryEntry{Kind: "peer_rejected", SessionID: c.Session.ID, SourceSessionID: sourceEvidence(f), Message: message, Error: err.Error()})
}

// noRouteNoticeInterval bounds how often one sender is told why its messages
// to a session with no reply route are refused, so a model that answers the
// notice cannot start an endless exchange with it. At most
// noRouteNoticeSenders senders are remembered at once; while that many are,
// a new sender gets only its refused receipt.
const (
	noRouteNoticeInterval = 10 * time.Minute
	noRouteNoticeSenders  = 256
)

var noRouteNotices = struct {
	sync.Mutex
	sent map[string]time.Time
}{sent: map[string]time.Time{}}

// takeNoRouteNotice reports whether the sender is due a notice now, and
// records it when it is.
func takeNoRouteNotice(from string, now time.Time) bool {
	noRouteNotices.Lock()
	defer noRouteNotices.Unlock()
	if last, ok := noRouteNotices.sent[from]; ok && now.Sub(last) < noRouteNoticeInterval {
		return false
	}
	for sender, last := range noRouteNotices.sent {
		if now.Sub(last) >= noRouteNoticeInterval {
			delete(noRouteNotices.sent, sender)
		}
	}
	if len(noRouteNotices.sent) >= noRouteNoticeSenders {
		return false
	}
	noRouteNotices.sent[from] = now
	return true
}

// rejectFrame tells a sender its frame was not delivered. Claude shows its
// model a fixed notice for each receipt status and never the receipt's
// detail, so a frame refused for having no reply route is followed by a
// message that gives the reason and where to send instead.
func rejectFrame(reg *registry.Registry, c Config, f frame, ownSocket string, err error) {
	var noRoute surface.NoReplyRouteError
	if !errors.As(err, &noRoute) {
		_ = sendReceipt(c.SocketDir, f.From, ownSocket, f.MsgID, "denied", err.Error())
		return
	}
	_ = sendReceipt(c.SocketDir, f.From, ownSocket, f.MsgID, "refused", err.Error())
	target := strings.TrimPrefix(f.From, "uds:")
	if validateTargetSocket(c.SocketDir, target) != nil {
		return
	}
	if !takeNoRouteNotice(f.From, time.Now()) {
		return
	}
	notice := fmt.Sprintf("Agenthail could not deliver your message %s. %s Messages to this address are refused the same way.", f.MsgID, err)
	payload, _ := json.Marshal(frame{MsgV: peerProtocol, MsgID: uuid.New().String(), Type: "user", Message: mustJSON(messageBody{Role: "user", Content: peerContent(c.Session, replyTarget(reg, c.Session.ID), ownSocket, "", notice)}), Priority: "next", From: "uds:" + ownSocket})
	if framed, err := authenticatedFrame(c.Home, target, payload); err == nil {
		_ = postFrame(target, framed)
	}
}

// recordReceipt keeps the delivery equal to what the target's Claude reported
// to its sender. A hold is left to the target's host, which knows its cause;
// the receipt's final outcome stands on its own.
func recordReceipt(reg *registry.Registry, message registry.PeerMessage, status string) {
	switch status {
	case "delivered":
		_, _ = reg.ReleasePeerMessage(message)
	case "expired", "refused", "denied", "dropped":
		_, _ = reg.DropPeerMessage(message, registry.PeerDroppedReason(status))
	}
}
