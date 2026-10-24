package surface

import "testing"

func TestNormalizeStatus(t *testing.T) {
	for input, want := range map[string]SessionStatus{
		"idle":        StatusIdle,
		"shell":       StatusIdle,
		"busy":        StatusBusy,
		"running":     StatusBusy,
		"active":      StatusBusy,
		"in_progress": StatusBusy,
		"inprogress":  StatusBusy,
		"waiting":     StatusBusy,
		"offline":     StatusOffline,
		"":            StatusUnknown,
		"paused":      StatusUnknown,
	} {
		if got := NormalizeStatus(input); got != want {
			t.Errorf("NormalizeStatus(%q) = %q, want %q", input, got, want)
		}
	}
}
