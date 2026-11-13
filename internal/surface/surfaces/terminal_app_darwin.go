package surfaces

import (
	"bytes"
	"encoding/binary"

	"golang.org/x/sys/unix"
)

func processEnv(pid int, name string) string {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return ""
	}
	argc := int(binary.NativeEndian.Uint32(raw))
	fields := bytes.Split(raw[4:], []byte{0})
	index := 1
	for index < len(fields) && len(fields[index]) == 0 {
		index++
	}
	index += argc
	if index >= len(fields) {
		return ""
	}
	return envValue(fields[index:], name)
}
