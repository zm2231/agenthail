package claudepeer

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// PeerName is the name Claude lists for an Agenthail peer.
func PeerName(kind surface.SurfaceKind, name string) string {
	return "agenthail/" + string(kind) + ": " + name
}

// replyTarget is how agenthail send on this host reaches the session, empty
// when it has no alias.
func replyTarget(reg *registry.Registry, id string) string {
	target, _ := reg.ReplyTarget(id)
	return target
}

// listedName is the name Claude lists for the session: its reply target,
// else its title.
func listedName(reg *registry.Registry, session surface.Session) string {
	if target := replyTarget(reg, session.ID); target != "" {
		return target
	}
	return session.Name
}

var closingPeerTag = regexp.MustCompile(`(?i)<(\s*/\s*cross-session-message\b)`)

// messageID is the delivery's own uuid when it has one, so a report on the
// message names the delivery it belongs to.
func messageID(id string) string {
	if _, err := uuid.Parse(id); err == nil {
		return id
	}
	return uuid.New().String()
}

// peerContent wraps a message from session. The sender is named by target,
// how a reply reaches it, which is never shortened; without one, by its
// title, shortened to 64 runes.
func peerContent(session surface.Session, target, socket, fromMode, message string) string {
	name := target
	if name == "" {
		name = strings.TrimSpace(session.Name)
		if runes := []rune(name); len(runes) > 64 {
			name = string(runes[:64]) + "…"
		}
	}
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '"' || r == '<' || r == '>' || unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Cs, unicode.Zl, unicode.Zp) {
			return -1
		}
		return r
	}, PeerName(session.Surface, name)))
	switch session.ID {
	case surface.PersonSessionID:
		message = session.Name + surface.PersonPeerPreamble + message
	case surface.CLISessionID:
		message = "Sent with agenthail send from outside any session; no one reads replies to it.\n\n" + message
	}
	message = closingPeerTag.ReplaceAllString(message, `<\$1`)
	mode := ""
	if fromMode == "bypass" || fromMode == "prompting" {
		mode = ` from-mode="` + fromMode + `"`
	}
	return fmt.Sprintf("<cross-session-message from=\"uds:%s\" from-session=\"%s\" from-name=\"%s\"%s>\n%s\n</cross-session-message>", socket, surface.PeerUUID(session.Surface, session.ID), name, mode, message)
}
