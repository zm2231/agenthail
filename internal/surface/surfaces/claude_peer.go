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

	"github.com/zm2231/agenthail/internal/claudepeer"
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
	if sender, senderSocket, found, err := c.nativeCaller(ctx, surface.SourceSessionID(ctx)); err != nil {
		return nil, err
	} else if found {
		return claudepeer.SendNative(ctx, c.home, *sender, senderSocket, socket, message)
	}
	return peerbridge.Send(ctx, c.home, surface.SourceSessionID(ctx), socket, message)
}

func (c *Claude) ResolveCaller(ctx context.Context, ancestorPIDs []int) (*surface.Session, bool, error) {
	byPID := make(map[int]bool, len(ancestorPIDs))
	for _, pid := range ancestorPIDs {
		byPID[pid] = true
	}
	sessions, err := c.List(ctx)
	if err != nil {
		return nil, false, err
	}
	valid := make(map[int]surface.Session, len(sessions))
	for _, session := range sessions {
		if session.Transport == "uds" {
			valid[session.PID] = session
		}
	}
	var matches []surface.Session
	for pid := range byPID {
		record, present := c.peerRecord(pid)
		if !present || str(record, "agenthail") == "peer-worker" {
			continue
		}
		session, ok := valid[pid]
		if !ok {
			return nil, false, fmt.Errorf("Claude ancestor PID %d has no validated messaging endpoint", pid)
		}
		matches = append(matches, session)
	}
	if len(matches) == 0 {
		return nil, false, nil
	}
	if len(matches) > 1 {
		return nil, false, fmt.Errorf("ambiguous Claude caller ancestry matches %d sessions", len(matches))
	}
	return &matches[0], true, nil
}

func (c *Claude) nativeCaller(ctx context.Context, id string) (*surface.Session, string, bool, error) {
	if id == "" {
		return nil, "", false, nil
	}
	sessions, err := c.List(ctx)
	if err != nil {
		return nil, "", false, err
	}
	for _, session := range sessions {
		if session.ID != id || session.Transport != "uds" {
			continue
		}
		record, present := c.peerRecord(session.PID)
		if !present || str(record, "agenthail") == "peer-worker" {
			continue
		}
		socket := c.peerSocket(ctx, record)
		if socket == "" {
			return nil, "", false, surface.DeliveryUnavailable(fmt.Errorf("Claude sender messaging socket is unavailable"))
		}
		return &session, socket, true, nil
	}
	return nil, "", false, nil
}

func (c *Claude) peerRecord(pid int) (map[string]any, bool) {
	data, err := os.ReadFile(filepath.Join(c.home, ".claude", "sessions", strconv.Itoa(pid)+".json"))
	if err != nil {
		return nil, false
	}
	var record map[string]any
	if json.Unmarshal(data, &record) != nil {
		return nil, false
	}
	return record, true
}

func nativeClaudeOnly(session *surface.Session) bool {
	return session.Transport == "uds" && !strings.HasPrefix(session.ID, "session_") && !strings.HasPrefix(session.ID, "cse_")
}
