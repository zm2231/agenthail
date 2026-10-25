package claudepeer

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func listenOwned(path string) (net.Listener, error) {
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("path already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		ln.Close()
		os.Remove(path)
		return nil, err
	}
	if unixListener, ok := ln.(*net.UnixListener); ok {
		unixListener.SetUnlinkOnClose(false)
	}
	return ln, nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

func socketIdentity(path string) (uint64, uint64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 {
		return 0, 0, fmt.Errorf("not a Unix socket: %s", path)
	}
	return uint64(stat.Dev), uint64(stat.Ino), nil
}

func removeOwnedSocket(path string, device, inode uint64) {
	info, err := os.Lstat(path)
	if err != nil {
		return
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok && info.Mode()&os.ModeSocket != 0 && ownedByCurrentUser(info) && uint64(stat.Dev) == device && uint64(stat.Ino) == inode {
		_ = os.Remove(path)
	}
}

func removeManifestSocket(path string, device, inode uint64) bool {
	if device == 0 || inode == 0 {
		_, err := os.Lstat(path)
		return os.IsNotExist(err)
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || !ownedByCurrentUser(info) || uint64(stat.Dev) != device || uint64(stat.Ino) != inode {
		return false
	}
	if !SocketRefusesConnections(path) {
		return false
	}
	removeErr := os.Remove(path)
	return removeErr == nil || os.IsNotExist(removeErr)
}

var dialSocket = func(path string) (net.Conn, error) {
	return net.DialTimeout("unix", path, 200*time.Millisecond)
}

// SocketRefusesConnections reports whether nothing listens at path. A
// filesystem can hand a freed inode to the next socket made at the same path,
// so a matching device and inode do not prove a socket is stale; only a
// refused connection does, and any other dial result keeps the socket.
func SocketRefusesConnections(path string) bool {
	connection, err := dialSocket(path)
	if err == nil {
		connection.Close()
		return false
	}
	return errors.Is(err, syscall.ECONNREFUSED)
}

func validateNativeSender(home, socketDir, from string) error {
	if !strings.HasPrefix(from, "uds:") {
		return errors.New("sender is not a uds address")
	}
	socket := strings.TrimPrefix(from, "uds:")
	if err := validateTargetSocket(socketDir, socket); err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(socket), ".sock"))
	if err != nil {
		return errors.New("sender socket has no valid owner pid")
	}
	_, record, found, err := nativeRecord(home, socket)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("sender has no current native Claude record")
	}
	if record.Agenthail != "" {
		return errors.New("sender is not a current native Claude endpoint")
	}
	if record.PID != pid || record.ProcStart == "" || !nativeProcessMatches(pid, record.ProcStart) {
		return errors.New("sender native process identity is stale")
	}
	return nil
}

func validateRelaySocket(socketDir, path string) error {
	return validateTargetSocket(socketDir, path)
}

// ClaudeSocketDirs returns the directories Claude Code binds peer sockets in:
// /tmp/cc-socks, and on Linux $XDG_RUNTIME_DIR/cc-socks or the default
// /run/user/<uid>/cc-socks, matching Claude Code's own default namespaces.
func ClaudeSocketDirs() []string {
	return claudeSocketDirsFor(runtime.GOOS, os.Getenv("XDG_RUNTIME_DIR"), os.Getuid())
}

func claudeSocketDirsFor(goos, runtimeDir string, uid int) []string {
	dirs := []string{DefaultSocketDir}
	if goos != "linux" {
		return dirs
	}
	if runtimeDir = strings.TrimSpace(runtimeDir); runtimeDir != "" {
		dirs = append(dirs, filepath.Join(runtimeDir, "cc-socks"))
	}
	if dir := filepath.Join("/run/user", strconv.Itoa(uid), "cc-socks"); !slices.Contains(dirs, dir) {
		dirs = append(dirs, dir)
	}
	return dirs
}

func validateTargetSocket(dir, target string) error {
	if dir == "" {
		dir = defaultSocketDir
	}
	cleanDir, _ := filepath.Abs(dir)
	cleanTarget, _ := filepath.Abs(target)
	targetDir := filepath.Dir(cleanTarget)
	if (targetDir != cleanDir && !slices.Contains(ClaudeSocketDirs(), targetDir)) || !strings.HasSuffix(filepath.Base(cleanTarget), ".sock") {
		return surface.DeliveryTerminal(errors.New("target socket is outside the Claude socket namespace"), surface.DeliveryInvalidRequest)
	}
	st, err := os.Lstat(cleanTarget)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	if st.Mode()&os.ModeSymlink != 0 || st.Mode()&os.ModeSocket == 0 {
		return surface.DeliveryTerminal(errors.New("target is not a socket"), surface.DeliveryInvalidRequest)
	}
	pidText := strings.TrimSuffix(filepath.Base(cleanTarget), ".sock")
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return surface.DeliveryTerminal(errors.New("target socket has no valid owner pid"), surface.DeliveryInvalidRequest)
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return surface.DeliveryUnavailable(fmt.Errorf("target owner is not running: %w", err))
	}
	if stat, ok := st.Sys().(*syscall.Stat_t); ok && uint32(stat.Uid) != uint32(os.Getuid()) {
		return surface.DeliveryTerminal(errors.New("target socket is not owned by this user"), surface.DeliveryAccessDenied)
	}
	return nil
}
