package claudepeer

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func acceptControls(wg *sync.WaitGroup, ln net.Listener, reg *registry.Registry, c Config, ownSocket string) {
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
		go func() { defer wg.Done(); defer func() { <-sem }(); handleControl(conn, reg, c, ownSocket) }()
	}
}

func handleControl(conn net.Conn, reg *registry.Registry, c Config, ownSocket string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(readDeadline))
	var req controlRequest
	err := json.NewDecoder(io.LimitReader(conn, maxLineBytes)).Decode(&req)
	resp := Response{}
	if err != nil {
		resp.Error = &ResponseError{Class: "terminal", Kind: string(surface.DeliveryInvalidRequest), Error: err.Error()}
	} else if req.SenderID != c.Session.ID || strings.TrimSpace(req.Message) == "" {
		resp.Error = &ResponseError{Class: "terminal", Kind: string(surface.DeliveryInvalidRequest), Error: "sender identity mismatch or empty message"}
	} else if err := validateTargetSocket(c.SocketDir, req.TargetSocket); err != nil {
		resp.Error = classifyControlError(err)
	} else {
		session, err := reg.Session(c.Session.ID)
		if err != nil {
			resp.Error = &ResponseError{Class: "terminal", Kind: string(surface.DeliveryTargetMissing), Error: "sender session is no longer registered"}
			_ = json.NewEncoder(conn).Encode(resp)
			return
		}
		id := messageID(req.MsgID)
		senderSocket := ownSocket
		if req.ReplySocket != "" {
			if err := validateRelaySocket(c.SocketDir, req.ReplySocket); err != nil {
				resp.Error = classifyControlError(err)
				_ = json.NewEncoder(conn).Encode(resp)
				return
			}
			senderSocket = req.ReplySocket
		}
		sender := *session
		if sender.ID == surface.PersonSessionID {
			if sender.Name = req.PersonName; sender.Name == "" {
				if sender.Name, err = reg.PersonName(); err != nil {
					resp.Error = classifyControlError(err)
					_ = json.NewEncoder(conn).Encode(resp)
					return
				}
			}
		}
		content := peerContent(sender, replyTarget(reg, sender.ID), senderSocket, "", req.Message)
		f, _ := json.Marshal(frame{MsgV: 1, MsgID: id, UUID: id, Type: "user", Message: mustJSON(messageBody{Role: "user", Content: content}), Priority: "next", From: "uds:" + senderSocket})
		var sendErr error
		if len(f) >= maxLineBytes {
			sendErr = surface.DeliveryTerminal(errors.New("Claude peer message exceeds the 64 KiB frame limit"), surface.DeliveryInvalidRequest)
		} else {
			sendErr = sendAuthenticatedFrame(c.Home, req.TargetSocket, f)
		}
		if err := sendErr; err != nil {
			resp.Error = classifyControlError(err)
		} else {
			resp.OK = true
			resp.Result = &surface.SendResult{UUID: id, Accepted: true}
		}
	}
	_ = json.NewEncoder(conn).Encode(resp)
}
