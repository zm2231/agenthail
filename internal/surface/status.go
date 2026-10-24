package surface

import "strings"

var normalizedStatuses = map[string]SessionStatus{
	"idle":        StatusIdle,
	"shell":       StatusIdle,
	"busy":        StatusBusy,
	"running":     StatusBusy,
	"active":      StatusBusy,
	"in_progress": StatusBusy,
	"inprogress":  StatusBusy,
	"waiting":     StatusBusy,
	"offline":     StatusOffline,
}

func NormalizeStatus(status string) SessionStatus {
	if normalized, ok := normalizedStatuses[strings.ToLower(status)]; ok {
		return normalized
	}
	return StatusUnknown
}
