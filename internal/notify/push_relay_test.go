package notify

import "testing"

func TestPushRelayURLUsesBuildDefaultAndEnvironmentOverride(t *testing.T) {
	previous := bundledPushRelayURL
	bundledPushRelayURL = "https://packaged.example.test/"
	t.Cleanup(func() { bundledPushRelayURL = previous })
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", "")
	if got := PushRelayURL(); got != "https://packaged.example.test" {
		t.Fatalf("packaged URL=%q", got)
	}
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", "https://self-hosted.example.test/")
	if got := PushRelayURL(); got != "https://self-hosted.example.test" {
		t.Fatalf("override URL=%q", got)
	}
}
