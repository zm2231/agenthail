package claudepeer

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func acceptPeers(wg *sync.WaitGroup, ln net.Listener, reg *registry.Registry, c Config, ownSocket string) {
	sem := make(chan struct{}, 8)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		select {
		case sem <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer func() { <-sem }(); handlePeer(conn, reg, c, ownSocket) }()
	}
}

func handlePeer(conn net.Conn, reg *registry.Registry, c Config, ownSocket string) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(firstLineDeadline))
	s := bufio.NewScanner(io.LimitReader(conn, maxLineBytes*maxFrames))
	s.Buffer(make([]byte, 1024), maxLineBytes)
	type rejection struct {
		frame frame
		err   error
	}
	var rejected []rejection
	var accepted []frame
	defer func() {
		conn.Close()
		for _, item := range accepted {
			_ = sendReceipt(c.SocketDir, item.From, ownSocket, item.MsgID, "received", "transport accepted; logical source queue updated")
		}
		for _, item := range rejected {
			rejectFrame(reg, c, item.frame, ownSocket, item.err)
		}
	}()
	for n := 0; n < maxFrames && s.Scan(); n++ {
		var f frame
		if json.Unmarshal(s.Bytes(), &f) != nil {
			continue
		}
		if f.Type == "auth" {
			continue
		}
		if f.Type == "control" {
			var receipt struct {
				Action   string `json:"action"`
				Status   string `json:"status"`
				Original string `json:"orig_msg_id"`
			}
			if json.Unmarshal(s.Bytes(), &receipt) == nil && receipt.Action == "peer_message_status" {
				_ = reg.RecordHistory(registry.HistoryEntry{Kind: "peer_receipt", SessionID: c.Session.ID, SourceSessionID: f.From, Result: receipt.Status + ":" + receipt.Original})
				recordReceipt(reg, registry.PeerMessage{MsgID: receipt.Original, SenderSessionID: c.Session.ID}, receipt.Status)
			}
			continue
		}
		if err := queueFrame(reg, c, f, ownSocket); err != nil {
			rejected = append(rejected, rejection{frame: f, err: err})
			recordRejection(reg, c, f, s.Bytes(), err)
		} else {
			accepted = append(accepted, f)
		}
	}
}

func queueFrame(reg *registry.Registry, c Config, f frame, ownSocket string) error {
	if f.Type != "user" || f.MsgID == "" || f.From == "" || len(f.Message) == 0 {
		return errors.New("invalid user frame")
	}
	if _, err := uuid.Parse(f.MsgID); err != nil {
		return errors.New("msg_id must be UUID")
	}
	if !strings.HasPrefix(f.From, "uds:") {
		return errors.New("from must be a uds address")
	}
	var body messageBody
	if err := json.Unmarshal(f.Message, &body); err != nil || body.Role != "user" || body.Content == "" {
		return errors.New("invalid user message")
	}
	target, err := reg.Session(c.Session.ID)
	if err != nil {
		return fmt.Errorf("canonical target is not registered: %w", err)
	}
	if err := surface.NoReplyRoute(target); err != nil {
		return err
	}
	key := "peer:" + target.ID + ":" + f.From + ":" + f.MsgID
	// The host that owns a session decides whether it can take a message.
	if owner, err := reg.SessionHost(target.ID); err != nil {
		return err
	} else if owner != "" {
		return queueHostFrame(reg, c, f, target.ID, key, body.Content)
	}
	if target.Status == surface.StatusOffline || surface.IsReadOnlySession(target) {
		reason := surface.ReadOnlySessionReason(target)
		if reason == "" {
			reason = "target session is offline"
		}
		return surface.DeliveryTerminal(errors.New(reason), surface.DeliveryAccessDenied)
	}
	if c.BusyDelivery != "" {
		if _, modeErr := deliverypolicy.Normalize(c.BusyDelivery); modeErr != nil {
			return modeErr
		}
	}
	queueID, err := reg.QueueMessageWithOptions(target.ID, "[Claude peer message from "+f.From+"]\n"+body.Content, key, surface.SendOptions{BusyDelivery: c.BusyDelivery})
	if err != nil {
		return err
	}
	return reg.RecordHistory(registry.HistoryEntry{Kind: "peer_received", SessionID: target.ID, SourceSessionID: sourceEvidence(f), QueueID: queueID, Message: body.Content, Result: "transport accepted; model completion pending"})
}

func sourceEvidence(f frame) string { return f.From }

// queueHostFrame holds a message for a session another host owns, to be sent
// to that host as this sender's own message once it is reachable. The sender
// is named as one of this host's sessions so the owner can reply across the
// pairing; a sender that is not a current native Claude endpoint goes as
// this host's operator.
func queueHostFrame(reg *registry.Registry, c Config, f frame, targetID, key, content string) error {
	send := registry.HostSend{SessionID: targetID, Message: content, Key: key}
	if sender, ok := nativeSenderSession(reg, c, f.From); ok {
		send.SourceSessionID = sender
	} else {
		send.Message = "[Claude peer message from " + f.From + "]\n" + content
	}
	queueID, err := reg.QueueHostSend(send)
	if err != nil {
		return err
	}
	return reg.RecordHistory(registry.HistoryEntry{Kind: "peer_received", SessionID: targetID, SourceSessionID: sourceEvidence(f), QueueID: queueID, Message: content, Result: "held for its host; sent once the host is reachable"})
}

// nativeSenderSession resolves a frame's sender to this host's registered
// Claude session behind its messaging socket.
func nativeSenderSession(reg *registry.Registry, c Config, from string) (string, bool) {
	if validateNativeSender(c.Home, c.SocketDir, from) != nil {
		return "", false
	}
	_, record, found, err := nativeRecord(c.Home, strings.TrimPrefix(from, "uds:"))
	if err != nil || !found || record.SessionID == "" {
		return "", false
	}
	session, err := reg.Session(record.SessionID)
	if err != nil {
		return "", false
	}
	if owner, err := reg.SessionHost(session.ID); err != nil || owner != "" {
		return "", false
	}
	return session.ID, true
}
