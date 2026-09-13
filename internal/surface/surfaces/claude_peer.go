package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/peerbridge"
	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) peerSocket(ctx context.Context, record map[string]any) string {
	path := str(record, "messagingSocketPath")
	pid, _ := record["pid"].(float64)
	if pid <= 0 || path != filepath.Join("/tmp/cc-socks", strconv.Itoa(int(pid))+".sock") || !claudeProcessAlive(int(pid)) {
		return ""
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return ""
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return ""
	}
	if started := str(record, "procStart"); started != "" {
		processCtx, cancel := context.WithTimeout(ctx, 500000000)
		defer cancel()
		command := exec.CommandContext(processCtx, "ps", "-o", "lstart=", "-p", strconv.Itoa(int(pid)))
		command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
		actual, err := command.Output()
		if err != nil || !sameClaudeProcessStart(str(record, "version"), started, string(actual)) {
			return ""
		}
	}
	return path
}

const claudeProcessStartLayout = "Mon Jan 2 15:04:05 2006"

func sameClaudeProcessStart(version, recorded, actual string) bool {
	return sameClaudeProcessStartInLocation(version, recorded, actual, time.Local)
}

func sameClaudeProcessStartInLocation(version, recorded, actual string, localLocation *time.Location) bool {
	var major, minor, patch int
	if n, err := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); err != nil || n != 3 {
		return false
	}
	recordedLocation := localLocation
	if major > 2 || major == 2 && (minor > 1 || minor == 1 && patch >= 267) {
		recordedLocation = time.UTC
	}
	recordedAt, recordedErr := time.ParseInLocation(claudeProcessStartLayout, strings.TrimSpace(recorded), recordedLocation)
	actualAt, actualErr := time.ParseInLocation(claudeProcessStartLayout, strings.TrimSpace(actual), time.UTC)
	return recordedErr == nil && actualErr == nil && recordedAt.Equal(actualAt)
}

func (c *Claude) sendPeer(ctx context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	data, err := os.ReadFile(filepath.Join(c.home, ".claude", "sessions", strconv.Itoa(session.PID)+".json"))
	if err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	if session.ID != str(record, "sessionId") && session.ID != str(record, "bridgeSessionId") {
		return nil, surface.DeliveryTerminal(fmt.Errorf("Claude PID no longer belongs to this session"), surface.DeliveryTargetMissing)
	}
	socket := c.peerSocket(ctx, record)
	if socket == "" {
		return nil, surface.DeliveryUnavailable(fmt.Errorf("Claude messaging socket is unavailable"))
	}
	return peerbridge.Send(ctx, c.home, surface.SourceSessionID(ctx), socket, message)
}

func nativeClaudeOnly(session *surface.Session) bool {
	return session.Transport == "uds" && !strings.HasPrefix(session.ID, "session_") && !strings.HasPrefix(session.ID, "cse_")
}
