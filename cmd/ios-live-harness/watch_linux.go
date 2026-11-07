package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type procBackend struct{}

func newBackend() backend { return procBackend{} }

func (procBackend) endpoints(pids []int) ([]endpoint, error) {
	inet, err := procInetSockets()
	if err != nil {
		return nil, err
	}
	unixPaths, unixPeers, err := procUnixSockets()
	if err != nil {
		return nil, err
	}
	var out []endpoint
	for _, pid := range pids {
		if exe, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe")); err == nil {
			out = append(out, endpoint{pid: pid, kind: "exe", path: strings.TrimSuffix(exe, " (deleted)")})
		}
		dir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			target, err := os.Readlink(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			inode, isSocket := strings.CutPrefix(target, "socket:[")
			if !isSocket {
				if strings.HasPrefix(target, "/") {
					out = append(out, endpoint{pid: pid, kind: "file", path: target})
				}
				continue
			}
			inode = strings.TrimSuffix(inode, "]")
			if e, ok := inet[inode]; ok {
				e.pid = pid
				out = append(out, e)
				continue
			}
			if path, ok := unixPaths[inode]; ok {
				out = append(out, endpoint{pid: pid, kind: "unix", path: path, peer: unixPaths[unixPeers[inode]]})
			}
		}
	}
	return out, nil
}

func procInetSockets() (map[string]endpoint, error) {
	out := map[string]endpoint{}
	for _, table := range []struct{ file, kind string }{
		{"/proc/net/tcp", "tcp"}, {"/proc/net/tcp6", "tcp"}, {"/proc/net/udp", "udp"}, {"/proc/net/udp6", "udp"},
	} {
		data, err := os.ReadFile(table.file)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) < 10 {
				continue
			}
			local, err1 := procAddr(fields[1])
			remote, err2 := procAddr(fields[2])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("parse %s: %q", table.file, line)
			}
			// State 0A is TCP_LISTEN.
			out[fields[9]] = endpoint{kind: table.kind, local: local, remote: remote, listening: table.kind == "tcp" && fields[3] == "0A"}
		}
	}
	return out, nil
}

// procAddr parses an address from /proc/net/tcp: the IP as 32-bit words in
// host byte order, then the port in hex.
func procAddr(field string) (netip.AddrPort, error) {
	ipHex, portHex, ok := strings.Cut(field, ":")
	if !ok {
		return netip.AddrPort{}, fmt.Errorf("no port")
	}
	raw, err := hex.DecodeString(ipHex)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, fmt.Errorf("bad address")
	}
	for i := 0; i < len(raw); i += 4 {
		binary.BigEndian.PutUint32(raw[i:], binary.LittleEndian.Uint32(raw[i:]))
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return netip.AddrPort{}, err
	}
	addr, _ := netip.AddrFromSlice(raw)
	return netip.AddrPortFrom(addr.Unmap(), uint16(port)), nil
}

// procUnixSockets maps every Unix socket inode to its bound path ("" when
// unbound) and, through ss, to its peer's inode.
func procUnixSockets() (map[string]string, map[string]string, error) {
	data, err := os.ReadFile("/proc/net/unix")
	if err != nil {
		return nil, nil, err
	}
	paths := map[string]string{}
	for _, line := range strings.Split(string(data), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		path := ""
		if len(fields) >= 8 && strings.HasPrefix(fields[7], "/") {
			path = fields[7]
		}
		paths[fields[6]] = path
	}
	out, err := exec.Command("ss", "-Hxan").Output()
	if err != nil {
		return nil, nil, fmt.Errorf("list Unix socket peers: %w", err)
	}
	peers := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		// netid state recv-q send-q local-path local-inode peer-path peer-inode
		if len(fields) >= 8 && fields[7] != "0" {
			peers[fields[5]] = fields[7]
		}
	}
	return paths, peers, nil
}
