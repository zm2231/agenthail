package surface

import (
	"strings"
	"unicode"
)

type AgentMessage struct {
	Direction string `json:"direction"`
	Kind      string `json:"kind"`
	Peer      string `json:"peer,omitempty"`
	Channel   string `json:"channel,omitempty"`
	Summary   string `json:"summary"`
	Body      string `json:"body,omitempty"`
}

const (
	AgentIn  = "in"
	AgentOut = "out"

	AgentKindPeer         = "peer"
	AgentKindTask         = "task"
	AgentKindContinuation = "continuation"
	AgentKindNotice       = "notice"
	AgentKindSend         = "send"

	OriginPerson           = "person"
	OriginTaskNotification = "task-notification"

	agentSummaryRunes = 400
)

var injectedNoticeKinds = map[string]string{
	OriginTaskNotification: AgentKindTask,
	"auto-continuation":    AgentKindContinuation,
	"agenthail-notice":     AgentKindNotice,
}

func PresentTimelineItem(item TimelineItem) TimelineItem {
	channel := ""
	if (item.Kind == "message" || item.Kind == "text") && item.Role == "user" && item.Origin == "" {
		if envelope, ok := ParsePeerEnvelope(item.Text); ok {
			item.Role, item.Origin, item.Sender, item.Title, item.Text = "peer", AgentKindPeer, envelope.Sender, envelope.Sender, envelope.Body
			channel = envelope.Channel
		}
	}
	if item.Kind == "message" && item.Role == "peer" && channel == "" {
		if envelope, ok := ParsePeerEnvelope(item.Text); ok && envelope.Channel != "" {
			item.Text, channel = envelope.Body, envelope.Channel
		}
	}
	item.Agent = agentMessage(item, channel)
	return item
}

func PresentExchanges(exchanges []Exchange) []Exchange {
	for index := range exchanges {
		if exchanges[index].Origin != "" || exchanges[index].Command {
			continue
		}
		if envelope, ok := ParsePeerEnvelope(exchanges[index].User); ok {
			exchanges[index].Origin, exchanges[index].Sender, exchanges[index].User = AgentKindPeer, envelope.Sender, envelope.Body
		}
	}
	return exchanges
}

func PresentTimelineItems(items []TimelineItem) []TimelineItem {
	for index := range items {
		items[index] = PresentTimelineItem(items[index])
	}
	return items
}

func agentMessage(item TimelineItem, channel string) *AgentMessage {
	switch {
	case item.Kind == "message" && item.Role == "peer":
		return &AgentMessage{Direction: AgentIn, Kind: AgentKindPeer, Peer: PeerDisplayName(item.Sender), Channel: channel, Summary: AgentSummary(item.Text)}
	case item.Kind == "event" && item.Role == "system" && injectedNoticeKinds[item.Origin] != "":
		lead, _, _ := strings.Cut(strings.TrimSpace(item.Text), "\n\n")
		return &AgentMessage{Direction: AgentIn, Kind: injectedNoticeKinds[item.Origin], Peer: item.Title, Summary: AgentSummary(lead)}
	case item.Kind == "toolCall" || item.Kind == "tool" || item.Kind == "command":
		send, ok := ParseOutgoingSend(item.Title, item.Text)
		if !ok {
			return nil
		}
		return &AgentMessage{Direction: AgentOut, Kind: AgentKindSend, Peer: send.Target, Summary: send.Summary, Body: send.Body}
	}
	return nil
}

func PeerDisplayName(sender string) string {
	if rest, ok := strings.CutPrefix(sender, "agenthail/"); ok {
		if _, name, found := strings.Cut(rest, ": "); found && strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	return strings.TrimSpace(sender)
}

func AgentSummary(text string) string {
	collapsed := strings.Join(strings.FieldsFunc(text, unicode.IsSpace), " ")
	runes := []rune(collapsed)
	if len(runes) <= agentSummaryRunes {
		return collapsed
	}
	return strings.TrimRightFunc(string(runes[:agentSummaryRunes]), unicode.IsSpace) + "…"
}
