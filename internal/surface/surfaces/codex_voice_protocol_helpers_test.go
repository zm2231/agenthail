//go:build voice_protocol

package surfaces

import (
	"os"
	"testing"
)

func installedRealtimeProbeParams(t *testing.T, threadID, attemptID, sdp string) map[string]any {
	t.Helper()
	prompt, err := os.ReadFile("../../voice/realtime-system-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"threadId": threadID, "realtimeSessionId": attemptID, "transport": map[string]any{"type": "webrtc", "sdp": sdp}, "version": "v3", "outputModality": "audio", "includeStartupContext": false, "prompt": string(prompt), "flushTranscriptTailOnSessionEnd": true, "clientManagedHandoffs": true, "backendReasoningStatus": false, "delegationAckFiller": false}
}
