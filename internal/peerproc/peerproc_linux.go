package peerproc

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func peerOf(fd int) (Peer, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return Peer{}, err
	}
	return Peer{PID: int(cred.Pid), UID: int(cred.Uid)}, nil
}

func Parent(pid int) (int, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, err
	}
	return statParent(string(data))
}

// The command name in /proc/<pid>/stat may hold spaces and parentheses, so
// fields are counted from its last closing parenthesis.
func statParent(stat string) (int, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, fmt.Errorf("malformed /proc stat")
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 2 {
		return 0, fmt.Errorf("malformed /proc stat")
	}
	return strconv.Atoi(fields[1])
}
