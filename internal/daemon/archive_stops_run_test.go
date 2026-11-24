package daemon

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestALeadArchivingItsWorkerOnAPairedHostStopsTheWorkersRun(t *testing.T) {
	h := newHostTargets(t, true)
	out, refused := callMCP(t, h.caller, "agenthail_archive", map[string]any{"target": "vps::@builder", "clientRequestId": "archive-builder"})
	if refused {
		t.Fatalf("archive refused: %v", out)
	}
	if got := h.remote.fake.stoppedRuns(); !slices.Equal(got, []string{"remote-c"}) {
		t.Fatalf("stopped runs on the paired host = %v, want remote-c's", got)
	}
	if archived, err := h.remote.reg.SessionArchived("remote-c"); err != nil || !archived {
		t.Fatalf("archived=%v err=%v on the owning host", archived, err)
	}
}

func TestAnArchiveWhoseRunCannotBeStoppedIsRefusedAndLeavesTheSessionUnarchived(t *testing.T) {
	h := newHostTargets(t, true)
	h.remote.fake.mu.Lock()
	h.remote.fake.stopErr = errors.New("the Claude host is not answering")
	h.remote.fake.mu.Unlock()
	out, refused := callMCP(t, h.caller, "agenthail_archive", map[string]any{"target": "vps::@builder", "clientRequestId": "archive-builder"})
	if !refused || !strings.Contains(out["message"].(string), "could not be stopped") {
		t.Fatalf("refused=%v out=%v, want the archive refused because the run could not be stopped", refused, out)
	}
	if archived, err := h.remote.reg.SessionArchived("remote-c"); err != nil || archived {
		t.Fatalf("archived=%v err=%v, want the session left unarchived", archived, err)
	}
}
