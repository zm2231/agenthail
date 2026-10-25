package claudepeer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

var heartbeatInterval = 2 * time.Second

type sessionRecord struct {
	PID                 int    `json:"pid"`
	SessionID           string `json:"sessionId"`
	Cwd                 string `json:"cwd"`
	StartedAt           int64  `json:"startedAt"`
	ProcStart           string `json:"procStart"`
	Version             string `json:"version"`
	PeerProtocol        int    `json:"peerProtocol"`
	Kind                string `json:"kind"`
	Entrypoint          string `json:"entrypoint"`
	MessagingSocketPath string `json:"messagingSocketPath"`
	Name                string `json:"name"`
	NameSince           int64  `json:"nameSince"`
	Status              string `json:"status"`
	UpdatedAt           int64  `json:"updatedAt"`
	StatusUpdatedAt     int64  `json:"statusUpdatedAt"`
	Agenthail           string `json:"agenthail"`
	ProcessToken        string `json:"processToken"`
}

func status(s surface.Session) string {
	return string(surface.NormalizeStatus(string(s.Status)))
}

func heartbeat(stop <-chan struct{}, path string, record sessionRecord, reg *registry.Registry, id string) {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			next := record
			if session, err := reg.Session(id); err == nil {
				next.Name = PeerName(session.Surface, listedName(reg, *session))
				next.Cwd = session.Cwd
				next.Status = status(*session)
			}
			_, statErr := os.Lstat(path)
			missing := os.IsNotExist(statErr)
			// Claude Code rewrites its own records only when they change, so an
			// unchanged peer record stays put instead of being rewritten each tick.
			if !missing && next.Name == record.Name && next.Cwd == record.Cwd && next.Status == record.Status {
				continue
			}
			next.UpdatedAt = now.UnixMilli()
			next.StatusUpdatedAt = next.UpdatedAt
			if missing {
				if writeExclusiveJSON(path, next) == nil {
					record = next
				}
				continue
			}
			tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
			f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				continue
			}
			encodeErr := json.NewEncoder(f).Encode(next)
			closeErr := f.Close()
			if encodeErr == nil && closeErr == nil && recordOwned(path, next) && os.Rename(tmp, path) == nil {
				record = next
			}
			_ = os.Remove(tmp)
		}
	}
}

func recordOwned(path string, record sessionRecord) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var current sessionRecord
	return json.Unmarshal(data, &current) == nil && current.Agenthail == record.Agenthail && current.PID == record.PID && current.SessionID == record.SessionID && current.ProcStart == record.ProcStart && current.ProcessToken == record.ProcessToken
}

func cleanupOwned(path string, record sessionRecord) {
	if recordOwned(path, record) {
		_ = os.Remove(path)
	}
}

func removeOwnedRecord(path string, manifest OwnershipManifest) {
	if !ownedRecordMatches(path, manifest) {
		return
	}
	_ = os.Remove(path)
}

func ownedRecordMatches(path string, manifest OwnershipManifest) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var record sessionRecord
	return json.Unmarshal(data, &record) == nil && record.Agenthail == "peer-worker" && record.PID == manifest.PID && record.ProcStart == manifest.ProcStart && record.ProcessToken == manifest.ProcessToken
}

// nativeRecord reads the session record of the Claude process serving
// socket, and the config root that holds it.
func nativeRecord(home, socket string) (string, sessionRecord, bool, error) {
	roots, err := surface.ClaudeConfigRoots(home)
	if err != nil {
		return "", sessionRecord{}, false, err
	}
	for _, path := range surface.ClaudeSessionRecordPaths(roots, strings.TrimSuffix(filepath.Base(socket), ".sock")) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var record sessionRecord
		if json.Unmarshal(data, &record) == nil && record.MessagingSocketPath == socket {
			return filepath.Dir(filepath.Dir(path)), record, true, nil
		}
	}
	return "", sessionRecord{}, false, nil
}
