package main

import (
	"fmt"
	"net/netip"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

type lsofBackend struct{}

func newBackend() backend { return lsofBackend{} }

type lsofRecord struct {
	pid                           int
	fd, kind, device, name, state string
}

func (lsofBackend) endpoints(pids []int) ([]endpoint, error) {
	out, err := exec.Command("lsof", "-n", "-P", "-w", "-T", "s", "-F", "pftdnT", "-p", sortedPIDs(pids)).Output()
	// lsof exits 1 when one of the processes has exited; the rest is still read.
	if err != nil && len(out) == 0 {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("lsof: %w", err)
		}
	}
	records := parseLsof(string(out))
	var peers map[string]string
	var found []endpoint
	program := map[int]bool{}
	for _, r := range records {
		// lsof lists a process's executable as its first txt descriptor.
		if r.fd == "txt" && !program[r.pid] && strings.HasPrefix(r.name, "/") {
			program[r.pid] = true
			found = append(found, endpoint{pid: r.pid, kind: "exe", path: r.name})
			continue
		}
		switch r.kind {
		case "IPv4", "IPv6":
			e, ok := lsofInet(r)
			if !ok {
				return nil, fmt.Errorf("lsof: cannot read address %q", r.name)
			}
			found = append(found, e)
		case "unix":
			e := endpoint{pid: r.pid, kind: "unix"}
			if peer, ok := strings.CutPrefix(r.name, "->"); ok {
				if peers == nil {
					if peers, err = lsofUnixNames(); err != nil {
						return nil, err
					}
				}
				e.peer = peers[peer]
			} else if strings.HasPrefix(r.name, "/") {
				e.path = r.name
			}
			found = append(found, e)
		case "REG", "DIR":
			if strings.HasPrefix(r.name, "/") {
				found = append(found, endpoint{pid: r.pid, kind: "file", path: r.name})
			}
		}
	}
	return found, nil
}

// parseLsof reads lsof -F output: a p line starts a process, an f line a
// descriptor, and the lines after it describe that descriptor.
func parseLsof(out string) []lsofRecord {
	var records []lsofRecord
	pid := 0
	var current *lsofRecord
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		value := line[1:]
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(value)
			current = nil
		case 'f':
			records = append(records, lsofRecord{pid: pid, fd: value})
			current = &records[len(records)-1]
		case 't':
			if current != nil {
				current.kind = value
			}
		case 'd':
			if current != nil {
				current.device = value
			}
		case 'n':
			if current != nil {
				current.name = value
			}
		case 'T':
			if current != nil && strings.HasPrefix(value, "ST=") {
				current.state = strings.TrimPrefix(value, "ST=")
			}
		}
	}
	return records
}

func lsofInet(r lsofRecord) (endpoint, bool) {
	kind := "tcp"
	if r.state == "" {
		kind = "udp"
	}
	localText, remoteText, connected := strings.Cut(r.name, "->")
	local, ok := lsofAddr(localText)
	if !ok {
		return endpoint{}, false
	}
	e := endpoint{pid: r.pid, kind: kind, local: local, listening: r.state == "LISTEN"}
	if connected {
		if e.remote, ok = lsofAddr(remoteText); !ok {
			return endpoint{}, false
		}
	}
	return e, true
}

func lsofAddr(text string) (netip.AddrPort, bool) {
	text = strings.Replace(text, "*:", "0.0.0.0:", 1)
	addr, err := netip.ParseAddrPort(text)
	if err != nil {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port()), true
}

// lsofUnixNames maps every Unix socket on the machine to its bound path, so a
// connected socket's peer address can be named.
func lsofUnixNames() (map[string]string, error) {
	out, err := exec.Command("lsof", "-n", "-P", "-w", "-U", "-F", "dn").Output()
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("lsof -U: %w", err)
	}
	names := map[string]string{}
	for _, r := range parseLsof(string(out)) {
		if r.device != "" && strings.HasPrefix(r.name, "/") {
			names[r.device] = r.name
		}
	}
	return names, nil
}

func sortedPIDs(pids []int) string {
	sort.Ints(pids)
	parts := make([]string, len(pids))
	for i, pid := range pids {
		parts[i] = strconv.Itoa(pid)
	}
	return strings.Join(parts, ",")
}
