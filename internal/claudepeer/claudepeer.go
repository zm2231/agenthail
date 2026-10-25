package claudepeer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/procstart"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type Config struct {
	Home         string
	RegistryPath string
	Session      surface.Session
	SocketDir    string
	ControlPath  string
	ManifestPath string
	Generation   string
	ProcessToken string
	ReplyRelay   bool
	BusyDelivery string
}

type Ready struct {
	PID         int    `json:"pid"`
	SocketPath  string `json:"socketPath"`
	ControlPath string `json:"controlPath"`
}

func RunWorker(ctx context.Context, config Config, parent io.Reader, ready io.Writer) error {
	c, err := normalizeConfig(config)
	if err != nil {
		return err
	}
	if c.ProcessToken != "" && os.Getenv("AGENTHAIL_PEER_TOKEN") != c.ProcessToken {
		return errors.New("Claude peer worker process token does not match its launch environment")
	}
	if c.ReplyRelay {
		return runReplyRelay(ctx, c, parent, ready)
	}
	pid := os.Getpid()
	procStart, err := procstart.Lstart(context.Background(), pid)
	if err != nil {
		return fmt.Errorf("read process start: %w", err)
	}
	reg, err := registry.Open(c.RegistryPath)
	if err != nil {
		return err
	}
	defer reg.Close()
	claudeID := surface.PeerUUID(c.Session.Surface, c.Session.ID)
	socketPath := filepath.Join(c.SocketDir, strconv.Itoa(pid)+".sock")
	controlPath := c.ControlPath
	if controlPath == "" {
		controlPath = WorkerControlPath(c.Home, c.Generation, pid)
	}
	manifestPath := c.ManifestPath
	if manifestPath == "" {
		manifestPath = WorkerManifestPath(c.Home, c.Generation, pid)
	}
	if err := os.MkdirAll(c.SocketDir, 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(controlPath), 0700); err != nil {
		return err
	}
	recordPath := filepath.Join(surface.ClaudeConfigDir(c.Home), "sessions", strconv.Itoa(pid)+".json")
	if err := requireAbsent(socketPath, controlPath, recordPath, manifestPath); err != nil {
		return err
	}
	pending := OwnershipManifest{Agenthail: "peer-worker", State: "starting", Generation: c.Generation, SourceID: c.Session.ID, PID: pid, ProcStart: procStart, ControlPath: controlPath, SocketPath: socketPath, RecordPath: recordPath}
	pending.ProcessToken = c.ProcessToken
	if err := writeExclusiveJSONAtomic(manifestPath, pending); err != nil {
		return fmt.Errorf("write pending peer ownership manifest: %w", err)
	}
	ln, err := listenOwned(socketPath)
	if err != nil {
		cleanupManifest(manifestPath, pending)
		return fmt.Errorf("listen peer socket: %w", err)
	}
	socketDevice, socketInode, err := socketIdentity(socketPath)
	if err != nil {
		ln.Close()
		cleanupManifest(manifestPath, pending)
		return fmt.Errorf("read peer socket identity: %w", err)
	}
	peerOwned := pending
	peerOwned.SocketDevice = socketDevice
	peerOwned.SocketInode = socketInode
	if err := replaceManifest(manifestPath, pending, peerOwned); err != nil {
		ln.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		cleanupManifest(manifestPath, pending)
		return fmt.Errorf("record peer socket ownership: %w", err)
	}
	pending = peerOwned
	cl, err := listenOwned(controlPath)
	if err != nil {
		ln.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		cleanupManifest(manifestPath, pending)
		return fmt.Errorf("listen control socket: %w", err)
	}
	started := time.Now().UnixMilli()
	record := sessionRecord{PID: pid, SessionID: claudeID, Cwd: c.Session.Cwd, StartedAt: started, ProcStart: procStart,
		Version: "agenthail", PeerProtocol: peerProtocol, Kind: "interactive", Entrypoint: "cli", MessagingSocketPath: socketPath,
		Name: PeerName(c.Session.Surface, listedName(reg, c.Session)), NameSince: started, Status: status(c.Session), UpdatedAt: started, StatusUpdatedAt: started, Agenthail: "peer-worker", ProcessToken: c.ProcessToken}
	controlDevice, controlInode, err := socketIdentity(controlPath)
	if err != nil {
		ln.Close()
		cl.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		return fmt.Errorf("read control socket identity: %w", err)
	}
	controlOwned := pending
	controlOwned.ControlDevice = controlDevice
	controlOwned.ControlInode = controlInode
	if err := replaceManifest(manifestPath, pending, controlOwned); err != nil {
		ln.Close()
		cl.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		removeOwnedSocket(controlPath, controlDevice, controlInode)
		cleanupManifest(manifestPath, pending)
		return fmt.Errorf("record control socket ownership: %w", err)
	}
	pending = controlOwned
	manifest := pending
	manifest.State = "ready"
	if err := replaceManifest(manifestPath, pending, manifest); err != nil {
		ln.Close()
		cl.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		removeOwnedSocket(controlPath, controlDevice, controlInode)
		cleanupManifest(manifestPath, pending)
		return fmt.Errorf("complete peer ownership manifest: %w", err)
	}
	if err := writeExclusiveJSONAtomic(recordPath, record); err != nil {
		ln.Close()
		cl.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		removeOwnedSocket(controlPath, controlDevice, controlInode)
		cleanupManifest(manifestPath, manifest)
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var listeners, handlers sync.WaitGroup
	listeners.Add(3)
	go func() {
		defer listeners.Done()
		heartbeat(ctx.Done(), recordPath, record, reg, c.Session.ID)
	}()
	go func() { defer listeners.Done(); acceptPeers(&handlers, ln, reg, c, socketPath) }()
	go func() { defer listeners.Done(); acceptControls(&handlers, cl, reg, c, socketPath) }()
	defer func() {
		cancel()
		ln.Close()
		cl.Close()
		listeners.Wait()
		handlers.Wait()
		cleanupOwned(recordPath, record)
		cleanupManifest(manifestPath, manifest)
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		removeOwnedSocket(controlPath, controlDevice, controlInode)
	}()
	if ready != nil {
		if err := json.NewEncoder(ready).Encode(Ready{PID: pid, SocketPath: socketPath, ControlPath: controlPath}); err != nil {
			return err
		}
	}
	parentDone := make(chan struct{})
	if parent != nil {
		go watchParent(parent, parentDone)
	}
	select {
	case <-ctx.Done():
	case <-parentDone:
	}
	return nil
}

func normalizeConfig(c Config) (Config, error) {
	if c.Home == "" {
		c.Home, _ = os.UserHomeDir()
	}
	if c.Home == "" || c.Session.ID == "" || c.Session.Surface == "" {
		return c, errors.New("home, session id, and session surface are required")
	}
	if c.SocketDir == "" {
		c.SocketDir = defaultSocketDir
	}
	if c.Generation == "" {
		c.Generation = "direct"
	}
	return c, nil
}

func watchParent(parent io.Reader, done chan<- struct{}) {
	_, _ = io.Copy(io.Discard, parent)
	close(done)
}
