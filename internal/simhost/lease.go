package simhost

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
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
	udid     string
	acquired time.Time
	lastUsed time.Time
	capAt    time.Time
	lock     *os.File
	launched map[string]bool
	refs     map[string]point
}

type LeaseView struct {
	Kind      string    `json:"kind"`
	Device    string    `json:"device"`
	UDID      string    `json:"udid"`
	Acquired  time.Time `json:"acquired"`
	IdleUntil time.Time `json:"idleUntil"`
	EndsAt    time.Time `json:"endsAt"`
	OutDir    string    `json:"outDir"`
}

func (h *Host) view(l *lease) LeaseView {
	return LeaseView{Kind: l.kind, Device: Kinds[l.kind].Name, UDID: l.udid, Acquired: l.acquired, IdleUntil: l.lastUsed.Add(IdleLimit), EndsAt: l.capAt, OutDir: h.ownerDir(l.owner)}
}

func (h *Host) lockPath(udid string) string {
	return filepath.Join(h.lockDir, "agenthail-simulator-"+udid+".lock")
}

func (h *Host) acquire(ctx context.Context, owner, kindName string) (LeaseView, error) {
	kind, ok := Kinds[kindName]
	if !ok {
		return LeaseView{}, refuse("invalid", "kind must be iphone or ipad.")
	}
	h.mu.Lock()
	for _, l := range h.leases {
		if l.kind == kindName {
			defer h.mu.Unlock()
			if l.owner == owner {
				l.lastUsed = h.now()
				return h.view(l), nil
			}
			return LeaseView{}, &Error{Code: "busy", Message: fmt.Sprintf("Another session holds the %s; try again later or use the other kind.", kind.Name), Detail: map[string]any{"heldSince": l.acquired, "endsBy": h.ends(l)}}
		}
	}
	h.mu.Unlock()
	d, err := h.resolve(ctx, kind)
	if err != nil {
		return LeaseView{}, err
	}
	file, err := os.OpenFile(h.lockPath(d.UDID), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return LeaseView{}, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return LeaseView{}, refuse("busy", "The %s is in use by a run outside Agenthail's leases, such as a test through scripts/ios-simulator.sh; try again later.", kind.Name)
		}
		return LeaseView{}, err
	}
	if err := h.boot(ctx, d.UDID); err != nil {
		file.Close()
		return LeaseView{}, err
	}
	now := h.now()
	l := &lease{owner: owner, kind: kindName, udid: d.UDID, acquired: now, lastUsed: now, capAt: now.Add(LeaseLimit), lock: file, launched: map[string]bool{}, refs: map[string]point{}}
	h.mu.Lock()
	defer h.mu.Unlock()
	if held := h.leases[d.UDID]; held != nil {
		file.Close()
		return LeaseView{}, refuse("busy", "Another session took the %s first.", kind.Name)
	}
	h.leases[d.UDID] = l
	return h.view(l), nil
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

func (h *Host) held(owner, kindName string) (*lease, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, l := range h.leases {
		if l.owner == owner && (kindName == "" || l.kind == kindName) {
			if kindName == "" && h.ownedKinds(owner) > 1 {
				return nil, refuse("invalid", "You hold both simulators; say which with kind.")
			}
			l.lastUsed = h.now()
			return l, nil
		}
	}
	return nil, refuse("not_held", "You hold no simulator%s; acquire one first.", map[bool]string{true: " of that kind", false: ""}[kindName != ""])
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

func (h *Host) renew(owner, kindName string) (LeaseView, error) {
	l, err := h.held(owner, kindName)
	if err != nil {
		return LeaseView{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	l.capAt = h.now().Add(LeaseLimit)
	return h.view(l), nil
}

func (h *Host) Release(owner, kindName string) []string {
	h.mu.Lock()
	var ended []*lease
	for udid, l := range h.leases {
		if l.owner == owner && (kindName == "" || l.kind == kindName) {
			ended = append(ended, l)
			delete(h.leases, udid)
		}
	}
	h.mu.Unlock()
	return h.end(ended)
}

func (h *Host) Sweep() []string {
	now := h.now()
	h.mu.Lock()
	var ended []*lease
	for udid, l := range h.leases {
		if !now.Before(h.ends(l)) {
			ended = append(ended, l)
			delete(h.leases, udid)
		}
	}
	h.mu.Unlock()
	return h.end(ended)
}

func (h *Host) end(ended []*lease) []string {
	kinds := make([]string, 0, len(ended))
	for _, l := range ended {
		h.cleanup(l)
		kinds = append(kinds, l.kind)
	}
	sort.Strings(kinds)
	return kinds
}

func (h *Host) cleanup(l *lease) {
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
