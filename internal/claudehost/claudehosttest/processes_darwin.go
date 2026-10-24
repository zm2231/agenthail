package claudehosttest

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

func processes() (map[int]string, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	out := map[int]string{}
	for _, info := range all {
		pid := int(info.Proc.P_pid)
		if command := commandLine(pid); command != "" {
			out[pid] = command
		}
	}
	return out, nil
}

func commandLine(pid int) string {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return ""
	}
	argc := int(binary.LittleEndian.Uint32(raw))
	_, rest, _ := bytes.Cut(raw[4:], []byte{0})
	rest = bytes.TrimLeft(rest, "\x00")
	args := make([]string, 0, argc)
	for len(args) < argc && len(rest) > 0 {
		var arg []byte
		arg, rest, _ = bytes.Cut(rest, []byte{0})
		args = append(args, string(arg))
	}
	return strings.Join(args, " ")
}
