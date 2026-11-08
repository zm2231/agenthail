package testkit

import (
	"io"
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

	attempts, restore := GuardDialsTo(guarded)
	defer restore()
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

func TestARestoredGuardLetsItsAddressBeDialedAgain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	_, restore := GuardDialsTo(server.Listener.Addr().String())
	restore()
	response, err := (&http.Client{}).Get(server.URL)
	if err != nil {
		t.Fatalf("a dial after the guard was restored was refused: %v", err)
	}
	response.Body.Close()
}

func TestGuardsRestoredOutOfOrderEachStopOnlyThemselves(t *testing.T) {
	outer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer outer.Close()
	inner := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer inner.Close()
	reaches := func(server *httptest.Server) bool {
		response, err := (&http.Client{}).Get(server.URL)
		if err == nil {
			response.Body.Close()
		}
		return err == nil
	}
	_, restoreOuter := GuardDialsTo(outer.Listener.Addr().String())
	innerAttempts, restoreInner := GuardDialsTo(inner.Listener.Addr().String())
	restoreOuter()
	if !reaches(outer) || reaches(inner) {
		t.Fatal("restoring the outer guard first must free its address and keep the inner guard")
	}
	if len(innerAttempts()) != 1 {
		t.Fatalf("inner attempts = %d", len(innerAttempts()))
	}
	restoreInner()
	if !reaches(outer) || !reaches(inner) {
		t.Fatal("with both guards restored neither address may be refused")
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

func TestAGuardedRunNeverTakesTheRealAddressAFreePortRepeats(t *testing.T) {
	standard, other := "127.0.0.1:1", "127.0.0.1:2"
	picks := []string{standard, standard, other}
	offerFreeAddresses(t, func() string { pick := picks[0]; picks = picks[1:]; return pick })
	listen := standard
	runAwayFromLiveDashboard(func() int { return 0 }, standard, &listen, io.Discard)
	if listen != other {
		t.Fatalf("the default names %s; a free port equal to the real address %s must be skipped", listen, standard)
	}
}

func TestAGuardedRunThatOnlyGetsTheRealAddressFailsWithoutRunning(t *testing.T) {
	standard := "127.0.0.1:1"
	offerFreeAddresses(t, func() string { return standard })
	listen, ran := standard, false
	var report strings.Builder
	if code := runAwayFromLiveDashboard(func() int { ran = true; return 0 }, standard, &listen, &report); code == 0 || ran {
		t.Fatalf("code = %d, ran = %v; a binary that can only listen on the real address must fail before running", code, ran)
	}
	if !strings.Contains(report.String(), standard) {
		t.Fatalf("report = %q, want it to name the real address", report.String())
	}
}

func offerFreeAddresses(t *testing.T, next func() string) {
	previous := freeAddress
	freeAddress = next
	t.Cleanup(func() { freeAddress = previous })
}
