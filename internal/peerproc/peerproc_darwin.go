package peerproc

import "golang.org/x/sys/unix"

func peerOf(fd int) (Peer, error) {
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return Peer{}, err
	}
	pid, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	if err != nil {
		return Peer{}, err
	}
	return Peer{PID: pid, UID: int(cred.Uid)}, nil
}

func Parent(pid int) (int, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	return int(info.Eproc.Ppid), nil
}
