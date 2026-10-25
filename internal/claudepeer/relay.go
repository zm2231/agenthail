package claudepeer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/procstart"
	"github.com/zm2231/agenthail/internal/registry"
)

func runReplyRelay(ctx context.Context, c Config, parent io.Reader, ready io.Writer) error {
	pid := os.Getpid()
	procStart, err := procstart.Lstart(context.Background(), pid)
	if err != nil {
		return fmt.Errorf("read relay process start: %w", err)
	}
	socketPath := filepath.Join(c.SocketDir, strconv.Itoa(pid)+".sock")
	manifestPath := c.ManifestPath
	if manifestPath == "" {
		manifestPath = WorkerManifestPath(c.Home, c.Generation, pid)
	}
	if err := os.MkdirAll(c.SocketDir, 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0700); err != nil {
		return err
	}
	if err := requireAbsent(socketPath, manifestPath); err != nil {
		return err
	}
	manifest := OwnershipManifest{Agenthail: "peer-relay", State: "starting", Generation: c.Generation, SourceID: c.Session.ID, ProcessToken: c.ProcessToken, PID: pid, ProcStart: procStart, SocketPath: socketPath}
	if err := writeExclusiveJSONAtomic(manifestPath, manifest); err != nil {
		return err
	}
	ln, err := listenOwned(socketPath)
	if err != nil {
		cleanupManifest(manifestPath, manifest)
		return err
	}
	socketDevice, socketInode, err := socketIdentity(socketPath)
	if err != nil {
		ln.Close()
		cleanupManifest(manifestPath, manifest)
		return err
	}
	manifest.SocketDevice, manifest.SocketInode, manifest.State = socketDevice, socketInode, "ready"
	if err := replaceManifest(manifestPath, OwnershipManifest{Agenthail: "peer-relay", State: "starting", Generation: c.Generation, SourceID: c.Session.ID, ProcessToken: c.ProcessToken, PID: pid, ProcStart: procStart, SocketPath: socketPath}, manifest); err != nil {
		ln.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		cleanupManifest(manifestPath, manifest)
		return err
	}
	reg, err := registry.Open(c.RegistryPath)
	if err != nil {
		ln.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		cleanupManifest(manifestPath, manifest)
		return err
	}
	defer reg.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var handlers sync.WaitGroup
	go acceptRelays(&handlers, ln, reg, c, socketPath)
	if ready != nil {
		if err := json.NewEncoder(ready).Encode(Ready{PID: pid, SocketPath: socketPath}); err != nil {
			return err
		}
	}
	parentDone := make(chan struct{})
	if parent != nil {
		go watchParent(parent, parentDone)
	}
	defer func() {
		cancel()
		ln.Close()
		handlers.Wait()
		cleanupManifest(manifestPath, manifest)
		removeOwnedSocket(socketPath, socketDevice, socketInode)
	}()
	select {
	case <-ctx.Done():
	case <-parentDone:
	}
	return nil
}

func acceptRelays(wg *sync.WaitGroup, ln net.Listener, reg *registry.Registry, c Config, ownSocket string) {
	sem := make(chan struct{}, 8)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		select {
		case sem <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer func() { <-sem }(); handleRelay(conn, reg, c, ownSocket) }()
	}
}

func handleRelay(conn net.Conn, reg *registry.Registry, c Config, ownSocket string) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(firstLineDeadline))
	s := bufio.NewScanner(io.LimitReader(conn, maxLineBytes*maxFrames))
	s.Buffer(make([]byte, 1024), maxLineBytes)
	for n := 0; n < maxFrames && s.Scan(); n++ {
		var f frame
		if json.Unmarshal(s.Bytes(), &f) != nil || f.Type == "auth" {
			continue
		}
		if f.Type != "user" {
			continue
		}
		if err := validateNativeSender(c.Home, c.SocketDir, f.From); err != nil {
			_ = sendReceipt(c.SocketDir, f.From, ownSocket, f.MsgID, "denied", err.Error())
			continue
		}
		if err := queueFrame(reg, c, f, ownSocket); err != nil {
			recordRejection(reg, c, f, s.Bytes(), err)
			rejectFrame(reg, c, f, ownSocket, err)
			continue
		}
		_ = sendReceipt(c.SocketDir, f.From, ownSocket, f.MsgID, "received", "transport accepted; logical source queue updated")
	}
}
