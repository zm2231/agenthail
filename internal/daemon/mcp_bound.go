package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/peerproc"
)

var (
	// mcpReadBound bounds a read-only tool call. Abandoning a read changes
	// nothing, so past it the caller gets a refusal it can retry.
	mcpReadBound = 30 * time.Second
	// mcpWriteBound bounds a call that changes state. Past it the outcome is
	// unknown: the work goes on, and a retry with the same clientRequestId
	// reads its result.
	mcpWriteBound = 60 * time.Second
	// mcpWaitMargin is how far past its own timeout a tool that waits by
	// design may run.
	mcpWaitMargin = 30 * time.Second
	// mcpCallerBound bounds naming the caller of a request.
	mcpCallerBound = 10 * time.Second
	// mcpSlowCall is how long a step runs before its time is logged.
	mcpSlowCall       = 5 * time.Second
	mcpStallDumpEvery = 10 * time.Minute
)

var mcpReadTools = map[string]bool{
	"agenthail_read":         true,
	"agenthail_sessions":     true,
	"agenthail_whoami":       true,
	"agenthail_capabilities": true,
	"agenthail_requests":     true,
	"agenthail_ledger_read":  true,
	"agenthail_projects":     true,
	"agenthail_history":      true,
	"agenthail_result":       true,
}

// mcpOwnTimeout is a tool input that waits by design for as long as it says.
type mcpOwnTimeout interface{ mcpTimeout() time.Duration }

type mcpStall struct {
	mu       sync.Mutex
	lastDump time.Time
}

// mcpStallError is work that ran past its bound.
type mcpStallError struct {
	phase string
	bound time.Duration
}

func (e *mcpStallError) Error() string {
	return fmt.Sprintf("%s did not finish within %s", e.phase, e.bound)
}

func (e *mcpStallError) refusal() *mcpRefusal {
	return &mcpRefusal{Code: "busy", Message: fmt.Sprintf("The daemon did not finish %s within %s; try again in a moment.", e.phase, e.bound), Retryable: true, Detail: map[string]any{"phase": e.phase}}
}

func mcpCallBound(tool string, input any) time.Duration {
	if own, ok := input.(mcpOwnTimeout); ok {
		return own.mcpTimeout() + mcpWaitMargin
	}
	if mcpReadTools[tool] {
		return mcpReadBound
	}
	return mcpWriteBound
}

// mcpStalledCall is what a caller is told when its call ran past its bound.
func mcpStalledCall(tool string, input any, stall *mcpStallError) *mcpRefusal {
	if _, waits := input.(mcpOwnTimeout); waits || mcpReadTools[tool] {
		return stall.refusal()
	}
	detail := map[string]any{"phase": stall.phase, "outcome": "unknown"}
	if write, ok := input.(interface{ writeRequest() mcpWrite }); ok && strings.TrimSpace(write.writeRequest().ClientRequestID) != "" {
		refusal := mcpUnknownOutcome()
		refusal.Detail = detail
		return refusal
	}
	return &mcpRefusal{Code: "busy", Message: fmt.Sprintf("The daemon did not finish %s within %s and it may still take effect; check before calling it again.", stall.phase, stall.bound), Detail: detail}
}

// withinBound runs work and stops waiting for it after bound. The registry's
// waits do not take a context, so work past its bound finishes on its own
// goroutine and its result is dropped.
func withinBound[T any](d *Daemon, ctx context.Context, phase string, bound time.Duration, work func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	started := time.Now()
	go func() {
		value, err := work(ctx)
		done <- outcome{value, err}
		if took := time.Since(started); took >= mcpSlowCall {
			d.log.Printf("MCP %s took %s", phase, took.Round(time.Millisecond))
		}
	}()
	select {
	case result := <-done:
		return result.value, result.err
	case <-ctx.Done():
		select {
		case result := <-done:
			return result.value, result.err
		default:
		}
		var zero T
		if ctx.Err() == context.DeadlineExceeded {
			d.noteMCPStall(phase, bound)
			return zero, &mcpStallError{phase: phase, bound: bound}
		}
		return zero, ctx.Err()
	}
}

// logMCPCall writes one line per tool call, so a slow or refused call names
// its tool, caller and path.
func (d *Daemon) logMCPCall(ctx context.Context, caller *mcpCaller, tool string, took time.Duration, refusal *mcpRefusal) {
	session := "-"
	if caller != nil {
		switch {
		case caller.extension != "":
			session = "extension:" + caller.extension
		default:
			session = d.resolveDisplay(caller.session.ID)
		}
	}
	path := "http"
	if _, ok := ctx.Value(mcpPeerKey{}).(peerproc.Peer); ok {
		path = "socket"
	}
	outcome := "ok"
	if refusal != nil {
		outcome = "refused:" + refusal.Code
		if phase, ok := refusal.Detail["phase"].(string); ok {
			outcome += ":" + phase
		}
	}
	d.log.Printf("mcp %s session=%s path=%s took=%dms outcome=%s", tool, session, path, took.Milliseconds(), outcome)
}

// noteMCPStall logs a call that ran past its bound and saves what every
// goroutine was doing then, at most once per mcpStallDumpEvery.
func (d *Daemon) noteMCPStall(phase string, bound time.Duration) {
	now := time.Now()
	d.mcpStall.mu.Lock()
	dump := d.mcpStall.lastDump.IsZero() || now.Sub(d.mcpStall.lastDump) >= mcpStallDumpEvery
	if dump {
		d.mcpStall.lastDump = now
	}
	d.mcpStall.mu.Unlock()
	if !dump {
		d.log.Printf("MCP %s ran past %s", phase, bound)
		return
	}
	path, err := writeGoroutineDump(now)
	if err != nil {
		d.log.Printf("MCP %s ran past %s; saving the goroutines failed: %s", phase, bound, err)
		return
	}
	d.log.Printf("MCP %s ran past %s; goroutines saved to %s", phase, bound, path)
}

func writeGoroutineDump(now time.Time) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".agenthail", "debug")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "stall-"+now.UTC().Format("20060102T150405Z")+".txt")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := pprof.Lookup("goroutine").WriteTo(file, 2); err != nil {
		return "", err
	}
	return path, file.Close()
}
