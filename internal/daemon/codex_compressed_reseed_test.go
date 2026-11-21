package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

const codexOldRollout = `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"type":"response_item","payload":{"id":"prompt","type":"message","role":"user","content":[{"type":"input_text","text":"old prompt"}]}}
{"type":"response_item","payload":{"id":"answer","type":"message","role":"assistant","content":[{"type":"output_text","text":"old answer"}]}}
{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}
`

type codexRolloutOnDisk struct {
	compressed    bool
	corrupt       bool
	body          string
	seededAsEmpty bool
}

func openCodexSessionFromRollout(t *testing.T, rollout codexRolloutOnDisk) (string, map[string]int, error) {
	t.Helper()
	codextest.Start(t, func(string, map[string]any) map[string]any { return nil })
	_, reg, _, _, _ := daemonFixture(t)
	id := uuid.Must(uuid.NewV7()).String()
	at := time.Now()
	dir := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", at.Format("2006"), at.Format("01"), at.Format("02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(rollout.body)
	if rollout.body == "" {
		data = []byte(`{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/work"}}` + "\n" + codexOldRollout)
	}
	path := filepath.Join(dir, "rollout-"+at.Format("2006-01-02T15-04-05")+"-"+id+".jsonl")
	if rollout.corrupt {
		path += ".zst"
	} else if rollout.compressed {
		encoder, err := zstd.NewWriter(nil)
		if err != nil {
			t.Fatal(err)
		}
		data = encoder.EncodeAll(data, nil)
		path += ".zst"
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, HasLocal: true}
	if err := reg.RecordObservedSession(session); err != nil {
		t.Fatal(err)
	}
	if rollout.seededAsEmpty {
		if err := reg.MarkSessionJournalSeedWithIdentity(id, true, ""); err != nil {
			t.Fatal(err)
		}
	}
	stream, openErr := newTestSourceManager(t, reg).prepareStream(context.Background(), &session, codexsurface.NewCodex("1"))
	if openErr == nil {
		defer stream.Cancel()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := reg.SessionJournalSeedStatus(id)
		if err != nil {
			t.Fatal(err)
		}
		bodies, _ := journalBodies(t, reg, id)
		settled := status == registry.SessionJournalSeedFailed || (status == registry.SessionJournalSeeded && (rollout.body != "" || bodies["old answer"] > 0))
		if openErr != nil || settled || time.Now().After(deadline) {
			return status, bodies, openErr
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestACodexSessionReseedsFromAPlainRollout(t *testing.T) {
	status, bodies, err := openCodexSessionFromRollout(t, codexRolloutOnDisk{})
	if err != nil || status != registry.SessionJournalSeeded || bodies["old prompt"] == 0 || bodies["old answer"] == 0 {
		t.Fatalf("a plain rollout seeded status=%s bodies=%v err=%v", status, bodies, err)
	}
}

func TestACodexSessionReseedsFromARolloutCodexCompressed(t *testing.T) {
	status, bodies, err := openCodexSessionFromRollout(t, codexRolloutOnDisk{compressed: true})
	if err != nil || status != registry.SessionJournalSeeded || bodies["old prompt"] == 0 || bodies["old answer"] == 0 {
		t.Fatalf("a compressed rollout seeded status=%s bodies=%v err=%v", status, bodies, err)
	}
}

func TestACodexSessionMarkedSeededWithAnEmptyJournalReseedsFromItsCompressedRollout(t *testing.T) {
	status, bodies, err := openCodexSessionFromRollout(t, codexRolloutOnDisk{compressed: true, seededAsEmpty: true})
	if err != nil || status != registry.SessionJournalSeeded || bodies["old prompt"] == 0 || bodies["old answer"] == 0 {
		t.Fatalf("a session stuck with an empty journal seeded status=%s bodies=%v err=%v", status, bodies, err)
	}
}

func TestACodexCompressedRolloutThatCannotBeReadFailsTheSeed(t *testing.T) {
	status, bodies, err := openCodexSessionFromRollout(t, codexRolloutOnDisk{body: "not zstd", corrupt: true})
	if err == nil || status != registry.SessionJournalSeedFailed {
		t.Fatalf("an unreadable compressed rollout seeded status=%s bodies=%v err=%v", status, bodies, err)
	}
}

func TestACodexRolloutWithNoTurnYetSeeds(t *testing.T) {
	status, bodies, err := openCodexSessionFromRollout(t, codexRolloutOnDisk{body: `{"type":"session_meta","payload":{"cwd":"/work"}}` + "\n" + `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}` + "\n"})
	if err != nil || status != registry.SessionJournalSeeded {
		t.Fatalf("a rollout with no turn yet seeded status=%s bodies=%v err=%v", status, bodies, err)
	}
}
