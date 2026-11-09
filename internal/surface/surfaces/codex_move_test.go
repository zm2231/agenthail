package surfaces

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/zm2231/agenthail/internal/surface"
)

func refusedCodexMove(t *testing.T, children, queued []any) surface.MoveRefusal {
	t.Helper()
	startManagedCodex(t, func(method string, params map[string]any) map[string]any {
		switch method {
		case "thread/loaded/list":
			return map[string]any{"data": []any{managedThreadID}}
		case "thread/read":
			return map[string]any{"thread": map[string]any{"id": managedThreadID, "source": "agenthail", "status": map[string]any{"type": "idle"}}}
		case "thread/turns/list":
			return map[string]any{"data": []any{}}
		case "thread/list":
			if params["ancestorThreadId"] != managedThreadID {
				t.Errorf("thread/list asked for %v", params)
			}
			return map[string]any{"data": children}
		case "thread/queue/list":
			return map[string]any{"data": queued}
		}
		return nil
	})
	codex := isolatedManagedRuntime(t)
	session := &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Source: "agenthail", Transport: codexTransportManaged, Cwd: t.TempDir()}
	_, err := codex.PrepareMove(context.Background(), session, surface.AdoptRequest{})
	var refusal surface.MoveRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("PrepareMove returned %v, want a refusal", err)
	}
	release, err := holdCodexThread(context.Background(), managedThreadID)
	if err != nil {
		t.Fatalf("a refused move kept the thread held: %v", err)
	}
	release()
	return refusal
}

func TestACodexThreadWithSubagentsIsRefusedNamingThem(t *testing.T) {
	refusal := refusedCodexMove(t, []any{map[string]any{"id": "child-one"}, map[string]any{"id": "child-two"}}, nil)
	if !strings.Contains(refusal.Reason, "child-one") || !strings.Contains(refusal.Reason, "child-two") {
		t.Fatalf("the refusal does not name the subagent threads: %s", refusal.Reason)
	}
}

func TestACodexThreadWithQueuedMessagesIsRefused(t *testing.T) {
	refusal := refusedCodexMove(t, nil, []any{map[string]any{"id": "q", "clientUserMessageId": "c", "input": []any{map[string]any{"type": "text", "text": "later"}}}})
	if !strings.Contains(refusal.Reason, "queued") {
		t.Fatalf("the refusal does not name the queue: %s", refusal.Reason)
	}
}

func TestAMoveIsRefusedWhenArchivingChangesTheRolloutItDeclared(t *testing.T) {
	var rollout string
	startManagedCodex(t, func(method string, params map[string]any) map[string]any {
		switch method {
		case "thread/loaded/list":
			return map[string]any{"data": []any{managedThreadID}}
		case "thread/read":
			return map[string]any{"thread": map[string]any{"id": managedThreadID, "source": "agenthail", "path": rollout, "cwd": "/srv/work", "status": map[string]any{"type": "idle"}}}
		case "thread/turns/list", "thread/list", "thread/queue/list":
			return map[string]any{"data": []any{}}
		case "thread/goal/get":
			return map[string]any{}
		case "thread/archive":
			file, err := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Error(err)
				return nil
			}
			_, _ = file.WriteString(`{"type":"event_msg","payload":{}}` + "\n")
			_ = file.Close()
			archived := filepath.Join(os.Getenv("CODEX_HOME"), "archived_sessions")
			_ = os.MkdirAll(archived, 0700)
			if err := os.Rename(rollout, filepath.Join(archived, filepath.Base(rollout))); err != nil {
				t.Error(err)
			}
			return map[string]any{}
		}
		return nil
	})
	rollout = filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "2026", "11", "09", "rollout-2026-11-09T10-00-00-"+managedThreadID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rollout, []byte(`{"type":"session_meta","payload":{"id":"`+managedThreadID+`"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	codex := isolatedManagedRuntime(t)
	session := &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Source: "agenthail", Transport: codexTransportManaged}
	out, err := codex.PrepareMove(context.Background(), session, surface.AdoptRequest{})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Release()
	if err := codex.ArchiveMove(managedThreadID, uuid.NewString()); err == nil || !strings.Contains(err.Error(), "no longer matches") {
		t.Fatalf("ArchiveMove returned %v after Codex changed the declared rollout", err)
	}
}

func TestAMovedThreadsOtherRolloutsCannotExceedTheDeclaredSize(t *testing.T) {
	current := "rollout-2026-11-09T10-00-00-" + managedThreadID + ".jsonl"
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for _, name := range []string{current, "rollout-2026-11-09T09-00-00-" + managedThreadID + "_019f004a-0000-7000-8000-000000000001.jsonl", "rollout-2026-11-09T08-00-00-" + managedThreadID + "_019f004a-0000-7000-8000-000000000002.jsonl"} {
		body := []byte("123456")
		if err := writer.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write(body)
	}
	_ = writer.Close()
	sum := sha256.Sum256([]byte("123456"))
	state, _ := json.Marshal(codexMoveState{Rollout: current})
	in := surface.MoveIn{MoveID: uuid.NewString(), Conversation: managedThreadID, Cwd: t.TempDir(), Size: 6, SHA256: hex.EncodeToString(sum[:]), PartsSize: 10, State: state}
	_, err := isolatedManagedRuntime(t).ReceiveMove(context.Background(), in, &archive, func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), "more than the 10 bytes") {
		t.Fatalf("ReceiveMove returned %v for rollouts past the declared size", err)
	}
}
