package daemon

import (
	"net/http"
	"regexp"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

const (
	connectionDropBatch   = 50
	connectionDropMaxSpan = 30 * 24 * time.Hour
)

var (
	connectionDropIDPattern   = regexp.MustCompile(`^[a-z0-9]{8,64}$`)
	connectionDropCodePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	connectionDropPaths       = map[string]bool{"connect": true, "stream": true, "stream probe": true, "request": true}
	connectionDropRecoveries  = map[string]bool{"": true, "connect": true, "snapshot": true, "event stream": true, "stream probe": true, "peer connected": true}
	connectionDropShown       = map[string]bool{"held": true, "offline": true}
	connectionDropNetworks    = map[string]bool{"": true, "wifi": true, "cellular": true, "wired": true, "other": true, "none": true}
)

func (d *Daemon) apiDeviceConnectionDropsHandler(w http.ResponseWriter, r *http.Request) {
	device, err := d.Registry.AuthenticateDevice(bearerToken(r), "read")
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "device_unauthorized", "A paired device token is required.")
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	defer r.Body.Close()
	var request struct {
		Drops []registry.ConnectionDrop `json:"drops"`
	}
	if err := decodeAPIV1JSON(w, r, &request, false); err != nil || len(request.Drops) > connectionDropBatch {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "The connection drops are invalid.")
		return
	}
	now := time.Now()
	valid := make([]registry.ConnectionDrop, 0, len(request.Drops))
	for _, drop := range request.Drops {
		if clamped, ok := validConnectionDrop(drop, now); ok {
			valid = append(valid, clamped)
		}
	}
	recorded, err := d.Registry.RecordConnectionDrops(device.ID, valid)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "connection_drops_failed", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]int{"recorded": recorded})
}

func validConnectionDrop(drop registry.ConnectionDrop, now time.Time) (registry.ConnectionDrop, bool) {
	if !connectionDropIDPattern.MatchString(drop.ID) || !registry.ValidHostID(drop.Host) ||
		(drop.Via != "" && (!registry.ValidHostID(drop.Via) || drop.Via == drop.Host)) ||
		!connectionDropPaths[drop.Path] || !connectionDropRecoveries[drop.RecoveredBy] || !connectionDropShown[drop.Shown] ||
		!connectionDropNetworks[drop.Network] || !connectionDropCodePattern.MatchString(drop.FirstError) ||
		!connectionDropCodePattern.MatchString(drop.LastError) || drop.Attempts < 1 || drop.Attempts > 100_000 ||
		!inRange(drop.ElapsedMs) || !inRange(drop.SinceForegroundMs) {
		return drop, false
	}
	if drop.RecoveredAt.IsZero() || drop.RecoveredAt.After(now) {
		drop.RecoveredAt = now
	}
	if drop.StartedAt.IsZero() || drop.StartedAt.After(drop.RecoveredAt) || drop.RecoveredAt.Sub(drop.StartedAt) > connectionDropMaxSpan {
		return drop, false
	}
	return drop, true
}

func inRange(ms *int64) bool {
	return ms == nil || (*ms >= 0 && *ms <= int64(connectionDropMaxSpan/time.Millisecond))
}
