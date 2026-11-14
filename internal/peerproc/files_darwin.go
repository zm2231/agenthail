package peerproc

import (
	"bytes"
	"encoding/binary"
	"syscall"
	"unsafe"
)

// libproc's proc_pidinfo and proc_pidfdinfo, called through libSystem the way
// golang.org/x/sys/unix calls it, so no cgo is needed.
const (
	procPIDListFDs          = 1
	procFDInfoSize          = 8
	proxFDTypeVnode         = 1
	procPIDFDVnodePathInfo  = 2
	vnodeFDInfoWithPathSize = 1200
	vnodeFDInfoPathOffset   = 176
)

//go:linkname syscall_syscall6 syscall.syscall6
func syscall_syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

var libproc_proc_pidinfo_trampoline_addr uintptr

//go:cgo_import_dynamic libproc_proc_pidinfo proc_pidinfo "/usr/lib/libSystem.B.dylib"

var libproc_proc_pidfdinfo_trampoline_addr uintptr

//go:cgo_import_dynamic libproc_proc_pidfdinfo proc_pidfdinfo "/usr/lib/libSystem.B.dylib"

func OpenFiles(pid int) ([]string, error) {
	size, err := pidInfo(pid, procPIDListFDs, nil)
	if err != nil {
		return nil, err
	}
	list := make([]byte, size+16*procFDInfoSize)
	size, err = pidInfo(pid, procPIDListFDs, list)
	if err != nil {
		return nil, err
	}
	info := make([]byte, vnodeFDInfoWithPathSize)
	var paths []string
	for at := 0; at+procFDInfoSize <= size; at += procFDInfoSize {
		fd := int32(binary.LittleEndian.Uint32(list[at:]))
		if binary.LittleEndian.Uint32(list[at+4:]) != proxFDTypeVnode {
			continue
		}
		if n, err := pidFDInfo(pid, fd, procPIDFDVnodePathInfo, info); err != nil || n < vnodeFDInfoWithPathSize {
			continue
		}
		path, _, _ := bytes.Cut(info[vnodeFDInfoPathOffset:], []byte{0})
		if len(path) > 0 {
			paths = append(paths, string(path))
		}
	}
	return paths, nil
}

func pidInfo(pid, flavor int, buffer []byte) (int, error) {
	var base unsafe.Pointer
	if len(buffer) > 0 {
		base = unsafe.Pointer(&buffer[0])
	}
	r, _, errno := syscall_syscall6(libproc_proc_pidinfo_trampoline_addr, uintptr(pid), uintptr(flavor), 0, uintptr(base), uintptr(len(buffer)), 0)
	return libprocResult(r, errno)
}

func pidFDInfo(pid int, fd int32, flavor int, buffer []byte) (int, error) {
	r, _, errno := syscall_syscall6(libproc_proc_pidfdinfo_trampoline_addr, uintptr(pid), uintptr(fd), uintptr(flavor), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
	return libprocResult(r, errno)
}

func libprocResult(r uintptr, errno syscall.Errno) (int, error) {
	n := int(int32(r))
	if n <= 0 {
		if errno != 0 {
			return 0, errno
		}
		return 0, syscall.ESRCH
	}
	return n, nil
}
