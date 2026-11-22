package daemon

import (
	"bytes"
	"errors"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/nodehost"
)

func pageDaemon(t *testing.T, home string) (*Daemon, *bytes.Buffer) {
	t.Helper()
	d, _, _ := ledgerDaemon(t)
	var logged bytes.Buffer
	d.log = log.New(&logged, "", 0)
	d.SetExtensionHost(home, func() (nodehost.Launch, error) {
		return nodehost.Launch{}, errors.New("no extension host in this test")
	})
	t.Cleanup(d.extensions.mounts.close)
	return d, &logged
}

func ledgerPageState(t *testing.T, d *Daemon) pageState {
	t.Helper()
	if err := d.syncExtensionMounts(); err != nil {
		t.Fatal(err)
	}
	return d.extensionsView().BuiltIn[extension.Ledger]
}

func TestDaemonsOnOneComputerTakeTheirOwnPortsAndKeepThemAcrossARestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	homeA, homeB := extensionTestHome(t), extensionTestHome(t)
	a, _ := pageDaemon(t, homeA)
	b, _ := pageDaemon(t, homeB)
	pageA, pageB := ledgerPageState(t, a), ledgerPageState(t, b)
	if pageA.Port == 0 || pageB.Port == 0 || pageA.Port == pageB.Port {
		t.Fatalf("pages on %d and %d, want a port of each daemon's own", pageA.Port, pageB.Port)
	}

	a.extensions.mounts.close()
	held, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(pageA.Port)))
	if err != nil {
		t.Fatal(err)
	}
	restarted, logged := pageDaemon(t, homeA)
	for range 2 {
		page := ledgerPageState(t, restarted)
		if want := "port " + strconv.Itoa(pageA.Port) + " is in use by pid " + strconv.Itoa(os.Getpid()) + " ("; page.Port != pageA.Port || !strings.HasPrefix(page.Error, want) {
			t.Fatalf("while another process holds its port the page is %+v, want port %d and an error starting %q", page, pageA.Port, want)
		}
	}
	if got := strings.Count(logged.String(), "is held by pid "+strconv.Itoa(os.Getpid())); got != 1 {
		t.Fatalf("the holder was logged %d times, want once:\n%s", got, logged.String())
	}
	held.Close()
	if page := ledgerPageState(t, restarted); page.Port != pageA.Port || page.Error != "" {
		t.Fatalf("once its port is free the page is %+v, want it back on %d", page, pageA.Port)
	}
}

func TestADaemonWithExtensionPortsOffListensOnNoExtensionPort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(extensionPortsEnv, "off")
	home := extensionTestHome(t)
	d, _ := pageDaemon(t, home)
	d.configureExtensionPorts()
	if page := ledgerPageState(t, d); page.Port != 0 || page.Error != extensionPortsOffError {
		t.Fatalf("with extension ports off the page is %+v", page)
	}
	if ports, err := extension.ReadMountPorts(home); err != nil || len(ports) != 0 {
		t.Fatalf("with extension ports off the daemon recorded ports %v, %v", ports, err)
	}
}
