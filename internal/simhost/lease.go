package simhost

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

type Error struct {
	Code    string
	Message string
	Detail  map[string]any
}

func (e *Error) Error() string { return e.Message }

func refuse(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

type point struct{ X, Y float64 }

type lease struct {
	owner    string
	kind     string
	device   string
	runtime  string
	udid     string
	acquired time.Time
	lastUsed time.Time
	capAt    time.Time
	lock     *os.File
	mu       sync.Mutex
	ended    bool
	launched map[string]bool
	refs     map[string]point
}

type LeaseView struct {
	Kind      string    `json:"kind"`
	Device    string    `json:"device"`
	Runtime   string    `json:"runtime,omitempty"`
	UDID      string    `json:"udid"`
	Acquired  time.Time `json:"acquired"`
	IdleUntil time.Time `json:"idleUntil"`
	EndsAt    time.Time `json:"endsAt"`
	OutDir    string    `json:"outDir,omitempty"`
}

func (h *Host) view(l *lease) LeaseView {
	return LeaseView{Kind: l.kind, Device: l.device, Runtime: l.runtimeLabel(), UDID: l.udid, Acquired: l.acquired, IdleUntil: l.lastUsed.Add(IdleLimit), EndsAt: l.capAt, OutDir: h.ownerDir(l.owner)}
}

func (l *lease) runtimeLabel() string {
	if version := runtimeVersion(l.runtime); version != "" {
		return "iOS " + version
	}
	return ""
}

func (l *lease) shared() bool { return l.device == Kinds[l.kind].Name }

func (l *lease) fits(kindName, want string) bool {
	if kindName != "" && l.kind != kindName {
		return false
	}
	if runtimeWanted(want) == "" {
		return true
	}
	return iosRuntime{Identifier: l.runtime, Version: runtimeVersion(l.runtime)}.matches(want)
}

func (l *lease) label() string {
	if l.shared() {
		return l.kind
	}
	return l.kind + " (" + l.runtimeLabel() + ")"
}

func (h *Host) lockPath(udid string) string {
	return filepath.Join(h.lockDir, "agenthail-simulator-"+udid+".lock")
}

func (h *Host) acquire(ctx context.Context, owner, kindName, want string) (LeaseView, error) {
	kind, ok := Kinds[kindName]
	if !ok {
		return LeaseView{}, refuse("invalid", "kind must be iphone or ipad.")
	}
	versioned := runtimeWanted(want) != ""
	if versioned {
		installed, err := h.iosRuntimes(ctx)
		if err != nil {
			return LeaseView{}, err
		}
		runtime, err := pickRuntime(installed, want)
		if err != nil {
			return LeaseView{}, err
		}
		want = runtime.Identifier
	}
	slot := func(l *lease) bool {
		if versioned {
			return l.kind == kindName && l.runtime == want
		}
		return l.kind == kindName && l.shared()
	}
	h.endExpired(slot)
	h.mu.Lock()
	for _, l := range h.leases {
		if slot(l) {
			defer h.mu.Unlock()
			return h.reuse(l, owner)
		}
	}
	h.mu.Unlock()
	d, err := h.resolve(ctx, kind, want)
	if err != nil {
		return LeaseView{}, err
	}
	h.mu.Lock()
	if l := h.leases[d.UDID]; l != nil {
		defer h.mu.Unlock()
		return h.reuse(l, owner)
	}
	h.mu.Unlock()
	file, err := os.OpenFile(h.lockPath(d.UDID), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return LeaseView{}, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return LeaseView{}, refuse("busy", "The %s is in use by a run outside Agenthail's leases, such as a test through scripts/ios-simulator.sh; try again later.", d.Name)
		}
		return LeaseView{}, err
	}
	if err := h.boot(ctx, d.UDID); err != nil {
		file.Close()
		return LeaseView{}, err
	}
	now := h.now()
	l := &lease{owner: owner, kind: kindName, device: d.Name, runtime: d.Runtime, udid: d.UDID, acquired: now, lastUsed: now, capAt: now.Add(LeaseLimit), lock: file, launched: map[string]bool{}, refs: map[string]point{}}
	h.mu.Lock()
	defer h.mu.Unlock()
	if held := h.leases[d.UDID]; held != nil {
		file.Close()
		return LeaseView{}, refuse("busy", "Another session took the %s first.", d.Name)
	}
	h.leases[d.UDID] = l
	return h.view(l), nil
}

// reuse is called with h.mu held.
func (h *Host) reuse(l *lease, owner string) (LeaseView, error) {
	if l.owner == owner {
		l.lastUsed = h.now()
		return h.view(l), nil
	}
	return LeaseView{}, &Error{Code: "busy", Message: fmt.Sprintf("Another session holds the %s; try again later or use another kind or runtime.", l.device), Detail: map[string]any{"heldSince": l.acquired, "endsBy": h.ends(l)}}
}

func (h *Host) ends(l *lease) time.Time {
	idle := l.lastUsed.Add(IdleLimit)
	if idle.Before(l.capAt) {
		return idle
	}
	return l.capAt
}

func (h *Host) boot(ctx context.Context, udid string) error {
	_, _ = h.run(ctx, "xcrun", "simctl", "boot", udid)
	_, err := h.run(ctx, "xcrun", "simctl", "bootstatus", udid, "-b")
	return err
}

func (h *Host) held(owner, kindName, want string) (*lease, error) {
	h.endExpired(func(l *lease) bool { return l.owner == owner })
	h.mu.Lock()
	defer h.mu.Unlock()
	var found *lease
	for _, l := range h.leases {
		if l.owner != owner || !l.fits(kindName, want) {
			continue
		}
		if found != nil {
			return nil, refuse("invalid", "You hold more than one simulator; say which with kind, and runtime for a device made for one.")
		}
		found = l
	}
	if found == nil {
		return nil, refuse("not_held", "You hold no simulator%s; acquire one first.", map[bool]string{true: " of that kind", false: ""}[kindName != "" || runtimeWanted(want) != ""])
	}
	found.lastUsed = h.now()
	return found, nil
}

func (h *Host) ownedKinds(owner string) int {
	n := 0
	for _, l := range h.leases {
		if l.owner == owner {
			n++
		}
	}
	return n
}

func (h *Host) renew(owner, kindName, want string) (LeaseView, error) {
	l, err := h.held(owner, kindName, want)
	if err != nil {
		return LeaseView{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	l.capAt = h.now().Add(LeaseLimit)
	return h.view(l), nil
}

func (h *Host) Release(owner, kindName string) []string {
	return h.end(h.detach(func(l *lease) bool { return l.owner == owner && (kindName == "" || l.kind == kindName) }))
}

func (h *Host) release(owner, kindName, want string) ([]string, error) {
	match := func(l *lease) bool { return l.owner == owner && l.fits(kindName, want) }
	if kindName != "" || runtimeWanted(want) != "" {
		h.mu.Lock()
		n := 0
		for _, l := range h.leases {
			if match(l) {
				n++
			}
		}
		h.mu.Unlock()
		if n > 1 {
			return nil, refuse("invalid", "That names more than one simulator you hold; say which with runtime, or release without kind to end them all.")
		}
	}
	return h.end(h.detach(match)), nil
}

type Detached struct{ leases []*lease }

func (h *Host) Detach(owner, kindName string) Detached {
	return Detached{h.detach(func(l *lease) bool { return l.owner == owner && (kindName == "" || l.kind == kindName) })}
}

func (h *Host) Sweep() []string {
	return h.end(h.detach(h.expired))
}

func (h *Host) endExpired(match func(*lease) bool) {
	h.end(h.detach(func(l *lease) bool { return match(l) && h.expired(l) }))
}

func (h *Host) expired(l *lease) bool {
	return !h.now().Before(h.ends(l))
}

func (h *Host) detach(match func(*lease) bool) []*lease {
	h.mu.Lock()
	defer h.mu.Unlock()
	var detached []*lease
	for udid, l := range h.leases {
		if match(l) {
			detached = append(detached, l)
			delete(h.leases, udid)
		}
	}
	return detached
}

func (h *Host) End(detached Detached) {
	h.end(detached.leases)
}

func (h *Host) end(ended []*lease) []string {
	kinds := make([]string, 0, len(ended))
	for _, l := range ended {
		h.cleanup(l)
		kinds = append(kinds, l.label())
	}
	sort.Strings(kinds)
	return kinds
}

// cleanup takes the lease's own lock, so it waits for an action still running and no action starts after it.
func (h *Host) cleanup(l *lease) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended {
		return
	}
	l.ended = true
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bundles := make([]string, 0, len(l.launched))
	for bundle := range l.launched {
		bundles = append(bundles, bundle)
	}
	sort.Strings(bundles)
	for _, bundle := range bundles {
		_, _ = h.run(ctx, "xcrun", "simctl", "terminate", l.udid, bundle)
	}
	for _, args := range [][]string{{"location", l.udid, "clear"}, {"status_bar", l.udid, "clear"}} {
		if _, err := h.run(ctx, "xcrun", append([]string{"simctl"}, args...)...); err != nil {
			log.Printf("simulator %s: reset %s after a lease: %v", l.udid, args[0], err)
		}
	}
	l.lock.Close()
}

func (h *Host) Holds(owner string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ownedKinds(owner) > 0
}
