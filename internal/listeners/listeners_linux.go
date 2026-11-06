package listeners

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const tcpListen = "0A"

func scan(variables []string) ([]Listener, error) {
	byInode := map[string]Listener{}
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if err := readTable(table, byInode); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	owners := socketOwners(byInode)
	found := make([]Listener, 0, len(byInode))
	for inode, listener := range byInode {
		if pid, ok := owners[inode]; ok {
			listener.PID = pid
			listener.Command, listener.Env = describe(pid, variables)
		}
		found = append(found, listener)
	}
	return found, nil
}

func readTable(path string, into map[string]Listener) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	lines := bufio.NewScanner(file)
	lines.Scan()
	for lines.Scan() {
		fields := strings.Fields(lines.Text())
		if len(fields) < 10 || fields[3] != tcpListen || fields[9] == "0" {
			continue
		}
		address, port, ok := parseAddress(fields[1])
		if ok {
			into[fields[9]] = Listener{Address: address, Port: port}
		}
	}
	return lines.Err()
}

func parseAddress(field string) (string, int, bool) {
	hexAddress, hexPort, ok := strings.Cut(field, ":")
	if !ok {
		return "", 0, false
	}
	raw, err := hex.DecodeString(hexAddress)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return "", 0, false
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return "", 0, false
	}
	for word := 0; word < len(raw); word += 4 {
		binary.BigEndian.PutUint32(raw[word:], binary.NativeEndian.Uint32(raw[word:]))
	}
	address, _ := netip.AddrFromSlice(raw)
	return address.Unmap().String(), int(port), true
}

func socketOwners(byInode map[string]Listener) map[string]int {
	owners := map[string]int{}
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		fds, _ := os.ReadDir(filepath.Join("/proc", entry.Name(), "fd"))
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join("/proc", entry.Name(), "fd", fd.Name()))
			if err != nil {
				continue
			}
			inode, ok := strings.CutPrefix(target, "socket:[")
			inode = strings.TrimSuffix(inode, "]")
			if _, listening := byInode[inode]; !ok || !listening {
				continue
			}
			if owner, seen := owners[inode]; !seen || pid < owner {
				owners[inode] = pid
			}
		}
	}
	return owners
}

func describe(pid int, variables []string) (string, map[string]string) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	command := ""
	if argv, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		first, _, _ := bytes.Cut(argv, []byte{0})
		command = filepath.Base(string(first))
	}
	if command == "" || command == "." {
		comm, _ := os.ReadFile(filepath.Join(dir, "comm"))
		command = strings.TrimSpace(string(comm))
	}
	env := map[string]string{}
	if raw, err := os.ReadFile(filepath.Join(dir, "environ")); err == nil {
		for entry := range bytes.SplitSeq(raw, []byte{0}) {
			name, value, _ := strings.Cut(string(entry), "=")
			env[name] = value
		}
	}
	return command, pick(env, variables)
}
