package main

import "testing"

func TestSidecarAvailableNeedsAnOverrideOffMacOS(t *testing.T) {
	for _, tc := range []struct {
		goos, sidecar string
		want          bool
	}{
		{"darwin", "", true},
		{"linux", "", false},
		{"linux", "/opt/agenthail/sidecar.py", true},
	} {
		if got := sidecarAvailable(tc.goos, tc.sidecar); got != tc.want {
			t.Fatalf("sidecarAvailable(%q, %q)=%v, want %v", tc.goos, tc.sidecar, got, tc.want)
		}
	}
}
