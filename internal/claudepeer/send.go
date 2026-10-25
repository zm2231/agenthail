package claudepeer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/unixjson"
)

func Send(ctx context.Context, controlPath, senderID, targetSocket, message string) (*surface.SendResult, error) {
	return SendWithReplySocket(ctx, controlPath, senderID, targetSocket, "", message)
}

func SendWithReplySocket(ctx context.Context, controlPath, senderID, targetSocket, replySocket, message string) (*surface.SendResult, error) {
	if err := ValidateSend(senderID, targetSocket, message); err != nil {
		return nil, err
	}
	request := controlRequest{TargetSocket: targetSocket, SenderID: senderID, Message: message, ReplySocket: replySocket, MsgID: surface.MessageUUID(ctx), PersonName: surface.PersonName(ctx)}
	var response Response
	if err := unixjson.Call(ctx, controlPath, readDeadline, maxLineBytes, request, &response); err != nil {
		var notSent *unixjson.NotSentError
		if errors.As(err, &notSent) {
			return nil, surface.DeliveryUnavailable(fmt.Errorf("peer control socket: %w", notSent.Err))
		}
		return nil, surface.DeliveryOutcomeUnknown(err)
	}
	return response.Outcome("malformed peer control response")
}

// SendNative sends as a Claude session through its own socket. fromMode is the
// sender's permission class as its runtime reports it, or "" when unknown:
// Claude holds a message to a session that bypasses permission prompts unless
// its sender states that it does too.
func SendNative(ctx context.Context, home string, sender surface.Session, senderSocket, targetSocket, fromMode, message string) (*surface.SendResult, error) {
	if err := ValidateSend(sender.ID, targetSocket, message); err != nil {
		return nil, err
	}
	if err := validateTargetSocket(defaultSocketDir, targetSocket); err != nil {
		return nil, err
	}
	id := messageID(surface.MessageUUID(ctx))
	content := peerContent(sender, surface.ReplyTarget(ctx), senderSocket, fromMode, message)
	payload, _ := json.Marshal(frame{MsgV: peerProtocol, MsgID: id, UUID: id, Type: "user", Message: mustJSON(messageBody{Role: "user", Content: content}), Priority: "next", From: "uds:" + senderSocket})
	if len(payload) >= maxLineBytes {
		return nil, surface.DeliveryTerminal(errors.New("Claude peer message exceeds the 64 KiB frame limit"), surface.DeliveryInvalidRequest)
	}
	if err := sendAuthenticatedFrame(home, targetSocket, payload); err != nil {
		return nil, err
	}
	return &surface.SendResult{UUID: id, Accepted: true}, nil
}

func ValidateSend(senderID, targetSocket, message string) error {
	if strings.TrimSpace(senderID) == "" || strings.TrimSpace(targetSocket) == "" || strings.TrimSpace(message) == "" {
		return surface.DeliveryTerminal(errors.New("sender, target socket, and message are required"), surface.DeliveryInvalidRequest)
	}
	req, err := json.Marshal(controlRequest{TargetSocket: targetSocket, SenderID: senderID, Message: message})
	if err != nil {
		return surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	}
	if len(req) >= maxLineBytes {
		return surface.DeliveryTerminal(errors.New("Claude peer message exceeds the 64 KiB frame limit"), surface.DeliveryInvalidRequest)
	}
	return nil
}
