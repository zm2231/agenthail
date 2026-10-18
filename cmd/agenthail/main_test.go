package main

import "testing"

func TestNotionAvailableNeedsASidecarOffMacOS(t *testing.T) {
	for _, tc := range []struct {
		goos, sidecar string
		want          bool
	}{
		{"darwin", "", true},
		{"linux", "", false},
		{"linux", "/opt/agenthail/sidecar.py", true},
	} {
		if got := notionAvailable(tc.goos, tc.sidecar); got != tc.want {
			t.Fatalf("notionAvailable(%q, %q)=%v, want %v", tc.goos, tc.sidecar, got, tc.want)
		}
	}
}
