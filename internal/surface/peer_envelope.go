package surface

import "strings"

type PeerEnvelope struct {
	Sender  string
	Channel string
	Body    string
}

const CLISenderName = "Agenthail CLI"

const PersonPeerPreamble = " wrote this in Agenthail. They read your reply here in this session; answer in your reply, not with SendMessage.\n\n"

const channelSeparator = " via #"

func ParsePeerEnvelope(text string) (PeerEnvelope, bool) {
	if body, ok := strings.CutPrefix(text, CLILabel); ok {
		return PeerEnvelope{Sender: CLISenderName, Body: body}, true
	}
	rest, ok := strings.CutPrefix(text, "[from ")
	if !ok {
		return PeerEnvelope{}, false
	}
	label, body, ok := strings.Cut(rest, "] ")
	if !ok || strings.TrimSpace(label) == "" || strings.ContainsAny(label, "\n]") {
		return PeerEnvelope{}, false
	}
	envelope := PeerEnvelope{Sender: label, Body: body}
	if at := strings.LastIndex(label, channelSeparator); at > 0 && at+len(channelSeparator) < len(label) {
		envelope.Sender, envelope.Channel = label[:at], label[at+len(channelSeparator):]
	}
	return envelope, true
}
