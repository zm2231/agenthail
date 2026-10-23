package daemon

import "testing"

func TestRemoteURLUsesTailscaleHTTPS(t *testing.T) {
	if value := remoteURL("agent.tailnet.ts.net", 7412); value != "https://agent.tailnet.ts.net:7412/" {
		t.Fatalf("URL=%q", value)
	}
}
