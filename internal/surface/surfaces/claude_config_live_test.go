package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/peerbridge"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestLiveAlternateClaudeConfigMessaging(t *testing.T) {
	root, target, binary := os.Getenv("AGENTHAIL_LIVE_CLAUDE_CONFIG_DIR"), os.Getenv("AGENTHAIL_LIVE_CLAUDE_TARGET"), os.Getenv("AGENTHAIL_LIVE_BINARY")
	if root == "" || target == "" || binary == "" {
		t.Skip("requires an explicitly authorized live target, config root, and built CLI")
	}
	home, err := os.MkdirTemp("/tmp", "ah-live-root-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv("AGENTHAIL_CLAUDE_CONFIG_DIRS", root)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	reg, err := registry.Open(filepath.Join(home, ".agenthail", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	adapter, err := NewClaudeWithConfigDirs("", home, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	session, err := adapter.Resolve(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if session.ConfigDir != root || session.Transport != "uds" {
		t.Fatalf("unexpected target %+v", session)
	}
	if err := reg.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	manager, err := peerbridge.Start(ctx, home, reg, binary)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	marker := "alternate-home-check-" + time.Now().UTC().Format("20060102T150405")
	message := "Local Agenthail bridge verification (" + marker + "). Please read only your current CLAUDE_CONFIG_DIR environment value and confirm which Claude config directory this session uses. Reply briefly in this session and to the sender; do not change files or interrupt your work."
	receipt, err := adapter.Send(ctx, session, message)
	if err != nil || receipt == nil || !receipt.Accepted {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	t.Logf("target=%s configDir=%s messageId=%s", session.ID, session.ConfigDir, receipt.UUID)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		page, err := adapter.ReadSession(ctx, session, surface.SessionReadRequest{Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		for _, exchange := range page.Exchanges {
			if strings.Contains(exchange.User, marker) && strings.Contains(exchange.Assistant, root) {
				t.Logf("observed reply: %s", exchange.Assistant)
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("message was accepted but a matching reply was not observed")
		case <-ticker.C:
		}
	}
}

func TestLiveAlternateClaudeConfigRead(t *testing.T) {
	root, target, marker := os.Getenv("AGENTHAIL_LIVE_CLAUDE_CONFIG_DIR"), os.Getenv("AGENTHAIL_LIVE_CLAUDE_TARGET"), os.Getenv("AGENTHAIL_LIVE_CLAUDE_READ_MARKER")
	if root == "" || target == "" || marker == "" {
		t.Skip("requires an explicit live target and existing message marker; sends nothing")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewClaudeWithConfigDirs("", home, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := adapter.Resolve(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	page, err := adapter.ReadSession(ctx, session, surface.SessionReadRequest{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, exchange := range page.Exchanges {
		if strings.Contains(exchange.User, marker) && strings.Contains(exchange.Assistant, root) {
			t.Logf("target=%s configDir=%s source=%s matched message=%s", session.ID, session.ConfigDir, page.Source, marker)
			return
		}
	}
	t.Fatal("no reply matched the existing message and config root")
}
