package hosts

import (
	"encoding/json"
	"strings"
)

// Separator joins a host and a session id at the API and CLI boundary. It
// never appears in stored keys: sessions are keyed by their raw id and owned
// through registry host_id.
const Separator = "::"

// RepositoryPrefix starts a project id that names the same project on every
// host, so it is never host-qualified even though it may contain Separator.
const RepositoryPrefix = "repo:"

func Qualify(hostID, sessionID string) string {
	if hostID == "" || sessionID == "" || strings.Contains(sessionID, Separator) || strings.HasPrefix(sessionID, RepositoryPrefix) {
		return sessionID
	}
	return hostID + Separator + sessionID
}

func Split(ref string) (host, target string, ok bool) {
	host, target, ok = strings.Cut(ref, Separator)
	if !ok || host == "" || strings.HasPrefix(ref, RepositoryPrefix) {
		return "", ref, false
	}
	return host, target, true
}

var qualifiedKeys = map[string]bool{"sessionId": true, "projectId": true, "parentId": true, "rootId": true, "fromSession": true, "toSession": true, "forkedFrom": true}

func sessionReferenceKey(key string) bool {
	return qualifiedKeys[key] || strings.HasSuffix(key, "SessionId")
}

// QualifyJSON rewrites a remote host's response for this daemon's clients:
// session reference fields (sessionId, any key ending in SessionId, parentId,
// rootId, fromSession, toSession) and the id of session-shaped objects
// (objects that also carry "surface") become host-qualified. Every other
// value passes through unchanged.
func QualifyJSON(hostID string, raw []byte) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(qualifyValue(hostID, value))
}

func qualifyValue(hostID string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		_, sessionShaped := typed["surface"]
		for key, item := range typed {
			if text, ok := item.(string); ok && (sessionReferenceKey(key) || (key == "id" && sessionShaped)) {
				typed[key] = Qualify(hostID, text)
				continue
			}
			typed[key] = qualifyValue(hostID, item)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = qualifyValue(hostID, item)
		}
		return typed
	default:
		return value
	}
}
