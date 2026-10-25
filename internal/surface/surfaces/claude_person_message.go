package surfaces

import (
	"regexp"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

var claudePeerSession = regexp.MustCompile(`from-session="([^"]*)"`)

var personPeerUUID = func() string {
	person, _ := surface.AgenthailSender(surface.PersonSessionID, "")
	return surface.PeerUUID(person.Surface, person.ID)
}()

func claudePersonWords(text, body string) (string, bool) {
	envelope := claudePeerEnvelope.FindStringSubmatch(text)
	if envelope == nil {
		return "", false
	}
	session := claudePeerSession.FindStringSubmatch(envelope[1])
	if session == nil || session[1] != personPeerUUID {
		return "", false
	}
	if name, words, found := strings.Cut(body, surface.PersonPeerPreamble); found && !strings.Contains(name, "\n") {
		return strings.TrimSpace(words), true
	}
	return body, true
}
