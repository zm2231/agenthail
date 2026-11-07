package testkit

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTheGuardRefusesTheGuardedAddressAndNoOther(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	guarded := listener.Addr().String()
	reached := false
	server := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer other.Close()

	attempts := GuardDialsTo(guarded)
	client := &http.Client{}
	if _, err := client.Get("http://" + guarded + "/"); err == nil || !strings.Contains(err.Error(), guarded) {
		t.Fatalf("dial of the guarded address = %v", err)
	}
	if reached {
		t.Fatal("the guarded address was reached")
	}
	stacks := attempts()
	if len(stacks) != 1 {
		t.Fatalf("attempts = %d: %v", len(stacks), stacks)
	}
	response, err := client.Get(other.URL)
	if err != nil {
		t.Fatalf("another address was refused: %v", err)
	}
	response.Body.Close()
	if len(attempts()) != 1 {
		t.Fatal("another address counted as an attempt")
	}
}

func TestAGuardedRunFailsWhenADialReachesTheRealAddress(t *testing.T) {
	standard := FreeLoopbackAddress()
	listen := standard
	var report strings.Builder
	code := runAwayFromLiveDashboard(func() int {
		if listen == standard {
			t.Error("the default still names the real address during the run")
		}
		_, _ = (&http.Client{}).Get("http://" + standard + "/")
		return 0
	}, standard, &listen, &report)
	if code == 0 {
		t.Fatal("a run that dialed the real address passed")
	}
	if !strings.Contains(report.String(), "1 dial(s) reached "+standard) {
		t.Fatalf("report = %q, want it to name the dialed address", report.String())
	}
}
