package cli

import (
	"testing"

	"github.com/zm2231/agenthail/internal/simhost"
)

func TestDoctorSimulatorsLine(t *testing.T) {
	for _, test := range []struct {
		status *simhost.Status
		want   string
	}{
		{nil, ""},
		{&simhost.Status{Ready: true, AXe: "1.8.0", Runtime: "iOS-27-0", Simslim: true}, "simulators: ready (AXe 1.8.0, iOS-27-0, slimmed with simslim)"},
		{&simhost.Status{Ready: true, AXe: "1.8.0", Runtime: "iOS-27-0", Simslim: true, Devices: []simhost.DeviceView{{Device: "Agenthail iPhone", Runtime: "iOS 27.0"}, {Device: "Agenthail iPhone (iOS 18.5)", Runtime: "iOS 18.5"}}}, "simulators: ready (AXe 1.8.0, iOS-27-0, slimmed with simslim)\n  Agenthail iPhone: iOS 27.0\n  Agenthail iPhone (iOS 18.5): iOS 18.5"},
		{&simhost.Status{Problem: "AXe, which taps, types and reads the screen, is not installed", Fix: "brew install cameroncooke/axe/axe"}, "simulators: unavailable to agents: AXe, which taps, types and reads the screen, is not installed; fix: brew install cameroncooke/axe/axe"},
	} {
		if got := simulatorsLine(test.status); got != test.want {
			t.Errorf("simulatorsLine(%+v) = %q, want %q", test.status, got, test.want)
		}
	}
}
