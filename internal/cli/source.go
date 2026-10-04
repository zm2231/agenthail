package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

type processIdentity struct {
	pid  int
	ppid int
	uid  uint32
}

type claudeCallerResolver interface {
	ResolveCaller(context.Context, []int) (*surface.Session, bool, error)
}

var processSnapshot = readProcessSnapshot

func readProcessSnapshot(ctx context.Context) ([]processIdentity, error) {
	command := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,uid=")
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	processes := make([]processIdentity, 0)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		uid, uidErr := strconv.ParseUint(fields[2], 10, 32)
		if pidErr != nil || ppidErr != nil || uidErr != nil || pid <= 0 {
			continue
		}
		processes = append(processes, processIdentity{pid: pid, ppid: ppid, uid: uint32(uid)})
	}
	return processes, nil
}

func (a *App) sourceSessionID(ctx context.Context, selector string) (string, error) {
	if selector == "" {
		if id := os.Getenv("AGENTHAIL_SESSION_ID"); id != "" {
			selector = id
		} else if id := os.Getenv("CLAUDE_SESSION_ID"); id != "" {
			selector = "claude:" + id
		} else if id, err := a.resolveClaudeCaller(ctx); err != nil {
			return "", err
		} else if id != "" {
			return id, nil
		} else if id := os.Getenv("CODEX_THREAD_ID"); id != "" {
			selector = "codex:" + id
		}
	}
	if selector == "" {
		return "", nil
	}
	if a.Registry != nil {
		registrySelector := strings.TrimPrefix(selector, "@")
		expectedSurface := ""
		if kind, target, qualified := strings.Cut(registrySelector, ":"); qualified {
			expectedSurface = strings.ToLower(kind)
			registrySelector = strings.TrimPrefix(target, "@")
		}
		if id, err := a.Registry.ResolveTarget(registrySelector); err == nil {
			kind, _, _, sessionErr := a.Registry.GetSession(id)
			if sessionErr == nil && (expectedSurface == "" || expectedSurface == strings.ToLower(kind)) {
				return id, nil
			}
		}
	}
	session, _, err := a.resolveTarget(ctx, selector)
	if err != nil {
		return "", fmt.Errorf("resolve sender (use --from surface:session): %w", err)
	}
	return session.ID, nil
}

func (a *App) resolveClaudeCaller(ctx context.Context) (string, error) {
	adapter, ok := a.surfaceByKind(surface.KindClaude).(claudeCallerResolver)
	if !ok {
		return "", nil
	}
	processes, err := processSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("inspect caller process ancestry: %w", err)
	}
	byPID := make(map[int]processIdentity, len(processes))
	for _, process := range processes {
		byPID[process.pid] = process
	}
	ancestorPIDs := make([]int, 0)
	seen := map[int]bool{}
	for pid := os.Getpid(); pid > 0 && !seen[pid]; {
		seen[pid] = true
		process, found := byPID[pid]
		if !found {
			break
		}
		if process.uid != uint32(os.Getuid()) {
			break
		}
		ancestorPIDs = append(ancestorPIDs, process.pid)
		pid = process.ppid
	}
	if len(ancestorPIDs) == 0 {
		return "", nil
	}
	session, found, err := adapter.ResolveCaller(ctx, ancestorPIDs)
	if err != nil {
		return "", fmt.Errorf("resolve Claude caller: %w", err)
	}
	if !found {
		return "", nil
	}
	if a.Registry != nil {
		if err := a.Registry.RegisterSession(*session); err != nil {
			return "", fmt.Errorf("register Claude caller: %w", err)
		}
	}
	return session.ID, nil
}
