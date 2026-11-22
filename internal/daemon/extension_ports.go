package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/portholder"
)

const (
	extensionPortsEnv          = "AGENTHAIL_EXTENSION_PORTS"
	extensionPortBaseEnv       = "AGENTHAIL_EXTENSION_PORT_BASE"
	remoteExtensionPortBaseEnv = "AGENTHAIL_REMOTE_EXTENSION_PORT_BASE"
	extensionPortsOffError     = "extension ports are off on this daemon (" + extensionPortsEnv + "=off)"
)

func (d *Daemon) configureExtensionPorts() {
	switch value := strings.TrimSpace(os.Getenv(extensionPortsEnv)); value {
	case "":
	case "off":
		d.extensionPortsOff = true
		d.log.Printf("extension ports: off; no extension page, mount or paired host's entry is served")
		return
	default:
		d.log.Printf("extension ports: %s=%s is not off; ignoring it", extensionPortsEnv, value)
	}
	if os.Getenv(extensionPortBaseEnv) == "" && os.Getenv(remoteExtensionPortBaseEnv) == "" {
		return
	}
	dashboardPort := 0
	if config, err := dashconfig.Load(); err == nil {
		if _, port, err := net.SplitHostPort(config.Listen); err == nil {
			dashboardPort, _ = strconv.Atoi(port)
		}
	}
	local, remote, problems := extensionPortBases(os.Getenv, dashboardPort)
	for _, problem := range problems {
		d.log.Printf("extension ports: %s", problem)
	}
	extensionPortBase, remoteExtensionPortBase = local, remote
}

// A pool is pinned only when its variable names one that fits; 0 lets the
// system assign each new port.
func extensionPortBases(getenv func(string) string, dashboardPort int) (int, int, []string) {
	var problems []string
	pick := func(name string, count int, taken ...[2]int) int {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			return 0
		}
		base, err := strconv.Atoi(value)
		problem := ""
		switch {
		case err != nil:
			problem = "is not a number"
		case base < 1024 || base+count > 65536:
			problem = fmt.Sprintf("needs %d ports from 1024 up to 65535", count)
		case dashboardPort >= base && dashboardPort < base+count:
			problem = fmt.Sprintf("would take the dashboard's port %d", dashboardPort)
		}
		for _, other := range taken {
			if problem == "" && other[0] != 0 && base < other[1] && other[0] < base+count {
				problem = fmt.Sprintf("overlaps the other extension pool, %d to %d", other[0], other[1]-1)
			}
		}
		if problem != "" {
			problems = append(problems, fmt.Sprintf("%s=%s %s; letting the system assign ports", name, value, problem))
			return 0
		}
		return base
	}
	local := pick(extensionPortBaseEnv, extensionPortCount)
	remote := pick(remoteExtensionPortBaseEnv, remoteExtensionPortCount, [2]int{local, local + extensionPortCount})
	return local, remote, problems
}

type portHolders struct {
	mu     sync.Mutex
	logged map[string]string
}

func (p *portHolders) note(key, holder string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.logged[key] == holder {
		return false
	}
	if p.logged == nil {
		p.logged = map[string]string{}
	}
	p.logged[key] = holder
	return true
}

func (p *portHolders) clear(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.logged, key)
}

// listenExtensionPort binds key's recorded port, or with none a new port:
// the first free one of a pinned pool, else one the system assigns. A recorded
// port another process holds is retried on the next sync; the error names the
// holder, and the log names it once for each holder.
func (d *Daemon) listenExtensionPort(key string, recorded, base, count int, taken map[int]bool) (net.Listener, int, error) {
	if recorded != 0 {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(recorded)))
		if err == nil {
			d.extensionPortHolders.clear(key)
			return listener, recorded, nil
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			return nil, 0, fmt.Errorf("port %d: %w", recorded, err)
		}
		holder := portholder.Describe(recorded)
		if holder == "" {
			holder = "a process that could not be named"
		}
		if d.extensionPortHolders.note(key, fmt.Sprintf("%d %s", recorded, holder)) {
			d.log.Printf("extension %s: its port %d is held by %s; it is served there once that process lets go", key, recorded, holder)
		}
		return nil, 0, fmt.Errorf("port %d is in use by %s", recorded, holder)
	}
	if base == 0 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, 0, err
		}
		return listener, listener.Addr().(*net.TCPAddr).Port, nil
	}
	for port := base; port < base+count; port++ {
		if taken[port] {
			continue
		}
		if listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
			return listener, port, nil
		}
	}
	return nil, 0, fmt.Errorf("no free port from %d to %d", base, base+count-1)
}
