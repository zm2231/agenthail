package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	if pid <= 0 || !slices.Contains(claudeSocketPaths(int(pid)), path) || !claudeProcessAlive(int(pid)) {
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
	if started := str(record, "procStart"); isClockTicks(started) {
		actual, err := procStartTicks(int(pid))
		if err != nil || actual != started {
			return ""
		}
	} else if started != "" {
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

// Claude Code puts peer sockets in $XDG_RUNTIME_DIR/cc-socks on Linux and
// /tmp/cc-socks elsewhere.
func claudeSocketPaths(pid int) []string {
	name := strconv.Itoa(pid) + ".sock"
	paths := []string{filepath.Join("/tmp/cc-socks", name)}
	if runtimeDir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); runtimeDir != "" {
		paths = append(paths, filepath.Join(runtimeDir, "cc-socks", name))
	}
	return paths
}

// On Linux Claude Code records procStart as the starttime field of
// /proc/<pid>/stat, in clock ticks since boot.
func isClockTicks(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func procStartTicks(pid int) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	return procStatStartTime(string(data))
}

func procStatStartTime(stat string) (string, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return "", fmt.Errorf("malformed /proc stat")
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 20 {
		return "", fmt.Errorf("malformed /proc stat")
	}
	return fields[19], nil
}

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
	data, err := os.ReadFile(filepath.Join(surface.ClaudeConfigDir(c.home), "sessions", strconv.Itoa(session.PID)+".json"))
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

// ResolveCaller checks every live process, so a later process sharing a
// session id still resolves to that session.
func (c *Claude) ResolveCaller(ctx context.Context, ancestorPIDs []int) (*surface.Session, bool, error) {
	processes, err := c.processes(ctx)
	if err != nil {
		return nil, false, err
	}
	valid := make(map[int]surface.Session, len(processes))
	for _, process := range processes {
		if process.session.Transport == "uds" {
			valid[process.session.PID] = process.session
		}
	}
	for _, pid := range ancestorPIDs {
		record, present := c.peerRecord(pid)
		if !present || str(record, "agenthail") == "peer-worker" {
			continue
		}
		session, ok := valid[pid]
		if !ok {
			return nil, false, fmt.Errorf("Claude ancestor PID %d has no validated messaging endpoint", pid)
		}
		return &session, true, nil
	}
	return nil, false, nil
}

// nativeCaller finds the sender's own messaging socket. The source carries
// only a session id, so when several live processes share it the sender's
// process is unknown and the reply is addressed to the session instead.
func (c *Claude) nativeCaller(ctx context.Context, id string) (*surface.Session, string, bool, error) {
	if id == "" {
		return nil, "", false, nil
	}
	processes, err := c.processes(ctx)
	if err != nil {
		return nil, "", false, err
	}
	var sender *surface.Session
	var record map[string]any
	for _, process := range processes {
		if process.session.ID != id || process.session.Transport != "uds" {
			continue
		}
		candidate, present := c.peerRecord(process.session.PID)
		if !present || str(candidate, "agenthail") == "peer-worker" {
			continue
		}
		if sender != nil {
			return nil, "", false, nil
		}
		session := process.session
		sender, record = &session, candidate
	}
	if sender == nil {
		return nil, "", false, nil
	}
	socket := c.peerSocket(ctx, record)
	if socket == "" {
		return nil, "", false, surface.DeliveryUnavailable(fmt.Errorf("Claude sender messaging socket is unavailable"))
	}
	return sender, socket, true, nil
}

func (c *Claude) peerRecord(pid int) (map[string]any, bool) {
	data, err := os.ReadFile(filepath.Join(surface.ClaudeConfigDir(c.home), "sessions", strconv.Itoa(pid)+".json"))
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
