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

func TestOnlyAReleaseBuildOrAnExplicitOptInUpgradesTheRegistry(t *testing.T) {
	for _, tc := range []struct {
		version, allow string
		want           bool
	}{
		{"v0.2.66", "", true},
		{"dev", "", false},
		{"dev", "1", true},
		{"dev", "yes", false},
	} {
		t.Setenv("AGENTHAIL_ALLOW_DEV_MIGRATION", tc.allow)
		if got := mayUpgradeRegistry(tc.version); got != tc.want {
			t.Fatalf("mayUpgradeRegistry(%q) with %q=%v, want %v", tc.version, tc.allow, got, tc.want)
		}
	}
}
