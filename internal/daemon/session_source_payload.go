package daemon

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type sessionJournalPayload struct {
	Context          *surface.ContextUsage    `json:"context,omitempty"`
	Goal             *surface.GoalState       `json:"goal"`
	Commands         []surface.RuntimeCommand `json:"commands,omitempty"`
	Unavailable      string                   `json:"unavailable,omitempty"`
	Role             string                   `json:"role,omitempty"`
	Origin           string                   `json:"origin,omitempty"`
	Sender           string                   `json:"sender,omitempty"`
	Title            string                   `json:"title,omitempty"`
	Status           string                   `json:"status,omitempty"`
	ResetsAt         string                   `json:"resetsAt,omitempty"`
	ItemID           string                   `json:"itemId"`
	ProviderKey      string                   `json:"providerKey,omitempty"`
	Version          uint64                   `json:"version"`
	Op               string                   `json:"op"`
	Kind             string                   `json:"kind"`
	TurnID           string                   `json:"turnId,omitempty"`
	MessageID        string                   `json:"messageId,omitempty"`
	CallID           string                   `json:"callId,omitempty"`
	Final            bool                     `json:"final,omitempty"`
	TS               string                   `json:"ts"`
	Body             string                   `json:"body,omitempty"`
	Truncated        bool                     `json:"truncated"`
	TruncationReason string                   `json:"truncationReason,omitempty"`
	BodyRef          string                   `json:"bodyRef,omitempty"`
	Reason           string                   `json:"reason,omitempty"`
	Attachment       *surface.Attachment      `json:"attachment,omitempty"`
	Files            []surface.AttachedFile   `json:"files,omitempty"`
	Turn             *surface.TurnMetadata    `json:"turn,omitempty"`
}

func (s *sessionSource) normalizeLocked(event surface.StreamEvent) (sessionJournalPayload, string) {
	providerKey := event.ProviderKey
	if strings.HasPrefix(providerKey, "renderer:") {
		providerKey = s.epoch + ":" + providerKey
	}
	itemID := event.ID
	if strings.HasPrefix(event.ProviderKey, "renderer:") {
		itemID = s.epoch + ":" + itemID
	}
	if event.Kind == "context" || event.Kind == "goal" || event.Kind == "commands" {
		itemID = "state:" + event.Kind
		providerKey = itemID
	}
	if itemID == "" {
		itemID = providerKey
	}
	if itemID == "" {
		s.anonymous++
		itemID = fmt.Sprintf("%s:anonymous:%d", s.epoch, s.anonymous)
		providerKey = itemID
	}
	if event.Operation == "phase" {
		itemID += ":phase:" + event.Kind
		if providerKey != "" {
			providerKey += ":phase:" + event.Kind
		}
	}
	op := event.Operation
	if op == "" {
		op = "append"
	}
	body := event.Text
	bodyKey := ""
	if providerKey != "" && !strings.HasPrefix(event.ProviderKey, "renderer:") && op == "upsert" && body != "" {
		bodyKey = providerKey
	}
	if op == "append" && providerKey != "" && !strings.HasPrefix(event.ProviderKey, "renderer:") {
		body = s.appendBodies[providerKey] + body
		bodyKey = providerKey
		op = "upsert"
	}
	at := event.Timestamp
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return sessionJournalPayload{ItemID: itemID, ProviderKey: providerKey, Version: event.Version, Op: op, Kind: event.Kind, TurnID: event.TurnID, MessageID: event.MessageID, CallID: event.CallID, Final: event.Final, TS: at.UTC().Format(time.RFC3339Nano), Body: body, Role: event.Role, Origin: event.Origin, Sender: event.Sender, Title: event.Title, Status: event.Status, ResetsAt: event.ResetsAt, Context: event.Context, Goal: event.Goal, Commands: commandsOf(event.Commands), Unavailable: unavailableOf(event.Commands), Attachment: event.Attachment, Files: event.Files, Truncated: event.Truncated, TruncationReason: event.TruncationReason, Turn: event.Turn}, bodyKey
}

func commandsOf(commands *surface.RuntimeCommands) []surface.RuntimeCommand {
	if commands == nil {
		return nil
	}
	return commands.Commands
}

func unavailableOf(commands *surface.RuntimeCommands) string {
	if commands == nil {
		return ""
	}
	return commands.Unavailable
}

func (s *sessionSource) commandsStored(commands *surface.RuntimeCommands) bool {
	entry, found, err := s.manager.registry.SessionJournalEntryByProviderKey(s.session.ID, "state:commands")
	if err != nil || !found {
		return false
	}
	var stored sessionJournalPayload
	if json.Unmarshal(entry.Payload, &stored) != nil {
		return false
	}
	return slices.Equal(stored.Commands, commandsOf(commands)) && stored.Unavailable == unavailableOf(commands)
}
