package portholder

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const tcpListen = "0A"

func Find(port int) ([]Holder, error) {
	var listening, other []string
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		l, o, err := socketInodes(table, port)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		listening, other = append(listening, l...), append(other, o...)
	}
	inodes := listening
	if len(inodes) == 0 {
		inodes = other
	}
	if len(inodes) == 0 {
		return nil, nil
	}
	want := map[string]bool{}
	for _, inode := range inodes {
		want["socket:["+inode+"]"] = true
	}
	var holders []Holder
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	for _, proc := range procs {
		pid, err := strconv.Atoi(proc.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", proc.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			if link, err := os.Readlink(filepath.Join("/proc", proc.Name(), "fd", fd.Name())); err == nil && want[link] {
				holders = append(holders, Holder{PID: pid, Command: command(proc.Name())})
				break
			}
		}
	}
	if len(holders) == 0 {
		return []Holder{{}}, nil
	}
	slices.SortFunc(holders, func(a, b Holder) int { return a.PID - b.PID })
	return holders, nil
}

func socketInodes(table string, port int) (listening, other []string, err error) {
	file, err := os.Open(table)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	suffix := ":" + strings.ToUpper(strconv.FormatInt(int64(port), 16))
	for len(suffix) < 5 {
		suffix = ":0" + suffix[1:]
	}
	scanner := bufio.NewScanner(file)
	scanner.Scan()
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 || !strings.HasSuffix(fields[1], suffix) || fields[9] == "0" {
			continue
		}
		if fields[3] == tcpListen {
			listening = append(listening, fields[9])
		} else {
			other = append(other, fields[9])
		}
	}
	return listening, other, scanner.Err()
}

func command(pid string) string {
	if data, err := os.ReadFile(filepath.Join("/proc", pid, "cmdline")); err == nil && len(data) > 0 {
		return clip(strings.ReplaceAll(strings.TrimRight(string(data), "\x00"), "\x00", " "))
	}
	if data, err := os.ReadFile(filepath.Join("/proc", pid, "comm")); err == nil {
		return clip(string(data))
	}
	return ""
}
