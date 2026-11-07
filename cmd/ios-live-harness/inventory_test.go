package main

import (
	"bytes"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTheWatcherListsWhatItSawAndReportsAViolationPlantedThroughTheBackend(t *testing.T) {
	other, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	go acceptAll(other)
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	pid := startHelper(t, "planted", "PORT="+strconv.Itoa(port), "TCP="+other.Addr().String())
	root := shortTemp(t)
	s := scope{name: "host", root: root, port: port, forbidden: []string{root + "-none"}, executables: helperProgram(t)}

	var out bytes.Buffer
	w := newWatcherTo(&out, newBackend(), 50*time.Millisecond, func() map[int]scope { return map[int]scope{pid: s} })
	w.start()
	time.Sleep(300 * time.Millisecond)
	w.halt()

	log := out.String()
	for _, want := range []string{
		"ios-live-harness: saw host: tcp listening on 127.0.0.1:" + strconv.Itoa(port),
		"ios-live-harness: saw host: tcp 127.0.0.1:",
		"ios-live-harness: isolation: host has a TCP peer on a port the harness did not record",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("missing %q in:\n%s", want, log)
		}
	}
	if blind := w.blind(); len(blind) != 0 {
		t.Fatalf("a watcher that read the host was called blind: %q", blind)
	}
	if !strings.Contains(w.summary(), "took ") || strings.Contains(w.summary(), "took 0 ") {
		t.Fatalf("summary does not count samples: %s", w.summary())
	}
	if code := verdict(&out, 0, w.violations(), w.blind()); code != exitIsolation {
		t.Fatalf("a planted violation exits %d, want %d", code, exitIsolation)
	}
}

func TestEachDescriptorIsListedOnceWhateverItsClientPort(t *testing.T) {
	var out bytes.Buffer
	inv := newInventory(&out)
	s := scope{name: "host", port: 7000, trap: 7001}
	for _, client := range []uint16{50001, 50002} {
		inv.note(s, endpoint{kind: "tcp", local: netip.MustParseAddrPort("127.0.0.1:7000"), remote: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), client)})
		inv.note(s, endpoint{kind: "tcp", local: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), client+10), remote: netip.MustParseAddrPort("127.0.0.1:7001")})
	}
	for _, lib := range []string{"/usr/lib/libSystem.B.dylib", "/usr/lib/dyld"} {
		inv.note(s, endpoint{kind: "file", path: lib})
	}
	want := []string{
		"ios-live-harness: saw host: tcp connection to its own port 127.0.0.1:7000 from 127.0.0.1",
		"ios-live-harness: saw host: tcp connection from 127.0.0.1 to 127.0.0.1:7001",
		"ios-live-harness: saw host: files under /usr/lib/",
	}
	if got := strings.Split(strings.TrimSpace(out.String()), "\n"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("listed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

type silentBackend struct{}

func (silentBackend) endpoints([]int) ([]endpoint, error) { return nil, nil }

func TestAWatcherThatSawNothingCannotPassARun(t *testing.T) {
	var out bytes.Buffer
	none := newWatcherTo(&out, silentBackend{}, 20*time.Millisecond, func() map[int]scope { return nil })
	none.start()
	time.Sleep(60 * time.Millisecond)
	none.halt()
	if blind := none.blind(); len(blind) != 1 || !strings.Contains(blind[0], "no samples") {
		t.Fatalf("a watcher with no hosts to read: %q", blind)
	}

	pid := os.Getpid()
	deaf := newWatcherTo(&out, silentBackend{}, 20*time.Millisecond, func() map[int]scope { return map[int]scope{pid: {name: "Live A", port: 7000}} })
	deaf.start()
	time.Sleep(60 * time.Millisecond)
	deaf.halt()
	blind := deaf.blind()
	if len(blind) != 1 || blind[0] != "the watcher never saw Live A's own listener on port 7000" {
		t.Fatalf("a watcher that never saw the host's listener: %q", blind)
	}

	for _, c := range []struct{ status, want int }{{0, exitIsolation}, {65, 65}, {exitSetup, exitSetup}} {
		if got := verdict(&out, c.status, nil, blind); got != c.want {
			t.Errorf("status %d with a blind watcher exits %d, want %d", c.status, got, c.want)
		}
	}
	if got := verdict(&out, exitSetup, []string{"a violation"}, blind); got != exitIsolation {
		t.Errorf("a violation exits %d, want %d whatever else happened", got, exitIsolation)
	}
}
