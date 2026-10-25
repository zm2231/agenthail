package claudepeer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func sendReceipt(socketDir, from, ownSocket, msgID, status, detail string) error {
	if !strings.HasPrefix(from, "uds:") {
		return errors.New("receipt address is not uds")
	}
	target := strings.TrimPrefix(from, "uds:")
	if err := validateTargetSocket(socketDir, target); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"type": "control", "action": "peer_message_status", "status": status, "orig_msg_id": msgID, "from": "uds:" + ownSocket, "detail": detail})
	return postFrame(target, payload)
}

// postFrame writes a frame nothing waits on, such as a receipt, and closes
// without waiting for the sender to close first: a sender that accepts and
// never closes would otherwise hold the connection that is answering it for
// the full read deadline on every frame.
func postFrame(target string, payload []byte) error {
	conn, err := net.DialTimeout("unix", target, noticeDeadline)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(noticeDeadline))
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return surface.DeliveryOutcomeUnknown(err)
	}
	return nil
}

func sendFrame(target string, payload []byte) error {
	conn, err := net.DialTimeout("unix", target, readDeadline)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(readDeadline))
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return surface.DeliveryOutcomeUnknown(err)
	}
	if unix, ok := conn.(*net.UnixConn); ok {
		_ = unix.CloseWrite()
	}
	b := make([]byte, 1)
	_, err = conn.Read(b)
	if err != nil && !errors.Is(err, io.EOF) {
		return surface.DeliveryOutcomeUnknown(err)
	}
	if err == nil {
		return surface.DeliveryOutcomeUnknown(errors.New("unexpected response bytes from Claude socket"))
	}
	return nil
}

func sendAuthenticatedFrame(home, target string, payload []byte) error {
	framed, err := authenticatedFrame(home, target, payload)
	if err != nil {
		return err
	}
	return sendFrame(target, framed)
}

// authenticatedFrame prefixes payload with the peer key Claude wrote beside
// the target process's session record, in whichever config root holds that
// record.
func authenticatedFrame(home, target string, payload []byte) ([]byte, error) {
	hash := sha256.Sum256([]byte(target))
	pid := strings.TrimSuffix(filepath.Base(target), ".sock")
	root, _, found, err := nativeRecord(home, target)
	if err != nil {
		return nil, surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	}
	if !found {
		root = surface.ClaudeConfigDir(home)
	}
	keyPath := filepath.Join(root, "sessions", pid+"."+hex.EncodeToString(hash[:])+".key")
	if info, err := os.Lstat(keyPath); err == nil {
		if !info.Mode().IsRegular() {
			return nil, surface.DeliveryTerminal(errors.New("invalid Claude peer key file"), surface.DeliveryAuthenticationNeeded)
		}
		var key struct {
			PeerToken string `json:"peerToken"`
		}
		f, err := os.Open(keyPath)
		if err != nil {
			return nil, surface.DeliveryUnavailable(err)
		}
		err = json.NewDecoder(io.LimitReader(f, 8192)).Decode(&key)
		f.Close()
		if err != nil || key.PeerToken == "" {
			return nil, surface.DeliveryTerminal(errors.New("invalid Claude peer key"), surface.DeliveryAuthenticationNeeded)
		}
		auth, _ := json.Marshal(map[string]string{"type": "auth", "token": key.PeerToken})
		payload = append(append(auth, '\n'), payload...)
	} else if !os.IsNotExist(err) {
		return nil, surface.DeliveryUnavailable(err)
	}
	return payload, nil
}
