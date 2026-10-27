package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func addClaudeRoot(t *testing.T, home string) string {
	t.Helper()
	root := filepath.Join(home, ".work", ".claude")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "config.json"), []byte(`{"claudeConfigDirs":["`+root+`"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeProjectEntry(t *testing.T, path, cwd string) {
	t.Helper()
	entry := `{"projects":{"` + cwd + `":{"hasTrustDialogAccepted":true,"mcpServers":{"docs":{"command":"docs-mcp"}}}}}`
	if err := os.WriteFile(path, []byte(entry), 0600); err != nil {
		t.Fatal(err)
	}
}

func transcriptUnder(root, cwd, conversation string) string {
	path := filepath.Join(root, "projects", regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(cwd, "-"), conversation+".jsonl")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func rootMoveNode(t *testing.T) (*moveNode, string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	node := newMoveNode(t)
	second := addClaudeRoot(t, node.host.Home)
	cwd, err := filepath.EvalSymlinks(node.host.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	return node, second, cwd
}

func TestAnIdleSessionMovesToAnotherAccountOnItsHostWithItsProjectConfig(t *testing.T) {
	node, second, cwd := rootMoveNode(t)
	session := startMovable(t, node)
	writeProjectEntry(t, filepath.Join(node.host.Home, ".claude.json"), cwd)

	result, err := node.daemon.moveSessionToRoot(context.Background(), sessionMoveRequest{SessionID: session.ID, Root: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Move.State != registry.MoveDone || result.Root != "work" || result.Stopped != "host" {
		t.Fatalf("result=%+v", result)
	}
	if transcriptUnder(filepath.Join(node.host.Home, ".claude"), cwd, session.ID) != "" || transcriptUnder(second, cwd, session.ID) == "" {
		t.Fatal("the transcript did not move to the other account")
	}
	if owner, _ := node.reg.SessionHost(session.ID); owner != "" {
		t.Fatalf("a same-host move gave the session to %q", owner)
	}
	spawns := node.host.Spawns(t, session.ID)
	if last := spawns[len(spawns)-1]; last.ConfigDir == nil || *last.ConfigDir != second {
		t.Fatalf("the session resumed in %v, want %s", last.ConfigDir, second)
	}
	source, _ := os.ReadFile(filepath.Join(node.host.Home, ".claude.json"))
	target, _ := os.ReadFile(filepath.Join(second, ".claude.json"))
	key := "projects." + strings.ReplaceAll(cwd, ".", `\.`)
	if !gjson.GetBytes(target, key).Exists() || gjson.GetBytes(target, key+".mcpServers").Raw != gjson.GetBytes(source, key+".mcpServers").Raw {
		t.Fatalf("the target account's project entry is %s", target)
	}
	if reason, _ := node.reg.SessionMoveRefusal(session.ID); reason != "" {
		t.Fatalf("a finished move still holds the session: %s", reason)
	}
}

func TestAMoveToAnUnknownOrTheSameAccountLeavesTheSessionWhereItWas(t *testing.T) {
	node, second, cwd := rootMoveNode(t)
	session := startMovable(t, node)
	for _, root := range []string{"nobody", "default"} {
		_, err := node.daemon.moveSessionToRoot(context.Background(), sessionMoveRequest{SessionID: session.ID, Root: root})
		var refused MoveRefusedError
		if !errors.As(err, &refused) {
			t.Fatalf("a move to %s: %v", root, err)
		}
	}
	if transcriptUnder(filepath.Join(node.host.Home, ".claude"), cwd, session.ID) == "" || transcriptUnder(second, cwd, session.ID) != "" {
		t.Fatal("a refused move took the transcript away")
	}
	if reason, _ := node.reg.SessionMoveRefusal(session.ID); reason != "" {
		t.Fatalf("a refused move still holds the session: %s", reason)
	}
}

func TestAMoveToAPairedHostLandsInTheAccountItNames(t *testing.T) {
	source, target, _, checkout := movePair(t)
	second := addClaudeRoot(t, target.host.Home)
	session := startMovable(t, source)
	resolved, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server", Root: "nobody"}); err == nil || !strings.Contains(err.Error(), "nobody") {
		t.Fatalf("a move into an account the target lacks: %v", err)
	}
	if path := claudeTranscriptIn(t, source.host.Home, session.ID); path == "" {
		t.Fatal("a refused move took the transcript away")
	}
	result, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server", Root: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Move.State != registry.MoveDone {
		t.Fatalf("result=%+v", result)
	}
	if transcriptUnder(second, resolved, session.ID) == "" || transcriptUnder(filepath.Join(target.host.Home, ".claude"), resolved, session.ID) != "" {
		t.Fatal("the session did not land in the named account on the target")
	}
	spawns := target.host.Spawns(t, session.ID)
	if len(spawns) != 1 || spawns[0].ConfigDir == nil || *spawns[0].ConfigDir != second {
		t.Fatalf("target spawns=%+v, want one resume in %s", spawns, second)
	}
}

func TestAMoveToAHostThatIgnoresTheAccountIsRefused(t *testing.T) {
	source, target, _, _ := movePair(t)
	addClaudeRoot(t, target.host.Home)
	session := startMovable(t, source)
	target.gate.rootlessPrepare.Store(true)
	_, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server", Root: "work"})
	if err == nil || !strings.Contains(err.Error(), "cannot place a moved session in a chosen account") {
		t.Fatalf("a move to a host that ignores its account: %v", err)
	}
	if path := claudeTranscriptIn(t, source.host.Home, session.ID); path == "" {
		t.Fatal("a refused move took the transcript away")
	}
	if spawns := target.host.Spawns(t, session.ID); len(spawns) != 0 {
		t.Fatalf("the target started the session: %+v", spawns)
	}
}

func TestADaemonThatStoppedDuringARootMoveSettlesItWhenItStarts(t *testing.T) {
	node, second, cwd := rootMoveNode(t)
	session := startMovable(t, node)
	self := node.daemon.hostIdentity().ID
	moveID := "6a1b0c9d-1111-4222-8333-944455550001"
	manifest, _ := json.Marshal(moveManifest{ID: moveID, Conversation: session.ID, SourceHost: self, Surface: surface.KindClaude, Root: "work"})
	if err := node.reg.BeginMoveOut(moveID, session.ID, self, manifest); err != nil {
		t.Fatal(err)
	}
	if err := node.claude.ArchiveMove(session.ID, moveID); err != nil {
		t.Fatal(err)
	}
	node.daemon.failInterruptedMoves()
	if move, _ := node.reg.SessionMove(moveID); move.State != registry.MoveFailed {
		t.Fatalf("move=%+v", move)
	}
	if transcriptUnder(filepath.Join(node.host.Home, ".claude"), cwd, session.ID) == "" || transcriptUnder(second, cwd, session.ID) != "" {
		t.Fatal("an unplaced move did not come back to its account")
	}
}
