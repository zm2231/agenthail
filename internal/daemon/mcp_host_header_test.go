package daemon

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestARemoteMCPRequestATunnelForwardsWithItsPublicHostIsToldWhichHostToSend(t *testing.T) {
	f := newRemoteFixture(t, "")
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.server.URL+"/mcp", strings.NewReader(mcpInitialize))
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "agent.example.com"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Cf-Ray", "8f00000000000000-SJC")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	listen := strings.TrimPrefix(f.server.URL, "http://")
	if response.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "originRequest.httpHostHeader: "+listen) {
		t.Fatalf("status=%d body=%q, want a refusal naming httpHostHeader %s", response.StatusCode, body, listen)
	}
}
