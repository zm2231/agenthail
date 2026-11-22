package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

type remoteMCPHealth struct {
	Mode       string                    `json:"mode"`
	Team       string                    `json:"team,omitempty"`
	AUD        string                    `json:"aud,omitempty"`
	Keys       registry.AccessKeys       `json:"keys"`
	Identities []registry.RemoteIdentity `json:"identities"`
	Unread     map[string]int            `json:"unread"`
	Problems   []string                  `json:"problems"`
}

func remoteMCPHealthOf(r *registry.Registry, now time.Time) (remoteMCPHealth, error) {
	health := remoteMCPHealth{Identities: []registry.RemoteIdentity{}, Unread: map[string]int{}, Problems: []string{}}
	team, aud, configured, err := r.AccessSettings()
	if err != nil {
		return health, err
	}
	if health.Identities, err = r.RemoteIdentities(); err != nil {
		return health, err
	}
	for _, identity := range health.Identities {
		if unread, err := r.UnreadInbox(identity.Name); err == nil {
			health.Unread[identity.Name] = unread
		}
	}
	switch {
	case len(health.Identities) == 0:
		health.Mode = "off"
	case configured:
		health.Mode = "access+token"
	default:
		health.Mode = "token only"
	}
	if !configured {
		return health, nil
	}
	health.Team, health.AUD = team, aud
	if health.Keys, err = r.AccessKeysStatus(); err != nil {
		return health, err
	}
	if health.Keys.Error != "" {
		health.Problems = append(health.Problems, "the last read of the Access keys failed: "+health.Keys.Error)
	}
	for _, identity := range health.Identities {
		if identity.ClientID == "" && identity.Email == "" {
			health.Problems = append(health.Problems, fmt.Sprintf("@%s has no Access binding, so behind Access it cannot get in; run agenthail identity remote %s --access-client-id <id>", identity.Name, identity.Name))
		}
		if !identity.LastSeen.IsZero() && (health.Keys.ReadAt.IsZero() || now.Sub(health.Keys.ReadAt) > 24*time.Hour) && now.Sub(identity.LastSeen) < 24*time.Hour {
			health.Problems = append(health.Problems, "a remote identity called in the last day and the Access keys were not read in that time")
			break
		}
	}
	return health, nil
}

func (h remoteMCPHealth) String() string {
	var lines []string
	switch h.Mode {
	case "off":
		lines = append(lines, "remote mcp: off (no identity has remote access)")
	case "token only":
		lines = append(lines, "remote mcp: token only (no Access settings); publish only /mcp to it")
	default:
		keys := "keys never read"
		if !h.Keys.ReadAt.IsZero() {
			keys = "keys read " + h.Keys.ReadAt.Local().Format(time.DateTime)
		}
		lines = append(lines, fmt.Sprintf("remote mcp: Access and token (team %s, audience %s), %s", h.Team, h.AUD, keys))
	}
	for _, identity := range h.Identities {
		lines = append(lines, fmt.Sprintf("  @%s: %s, last seen %s, %d unread", identity.Name, identity.Binding(), lastSeen(identity.LastSeen), h.Unread[identity.Name]))
	}
	for _, problem := range h.Problems {
		lines = append(lines, "  problem: "+problem)
	}
	return strings.Join(lines, "\n")
}
