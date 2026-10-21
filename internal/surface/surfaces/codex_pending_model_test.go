package surfaces

import (
	"context"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

func pendingModelThread(t *testing.T, respond codextest.Responder, loaded bool) (*codextest.Daemon, *Codex, *surface.Session) {
	t.Helper()
	fake := startManagedCodex(t, respond)
	fake.AddThread(codextest.Thread{ID: "thread", Originator: "agenthail", Model: "gpt-luna"})
	if loaded {
		fake.Load("thread")
	}
	return fake, isolatedManagedRuntime(t), managedSession()
}

func lastTurnModel(t *testing.T, fake *codextest.Daemon) (any, string) {
	t.Helper()
	starts := fake.Calls("turn/start")
	if len(starts) == 0 {
		t.Fatalf("no turn/start; methods=%v", fake.Methods())
	}
	thread, _ := fake.Thread("thread")
	return starts[len(starts)-1]["model"], thread.Turns[len(thread.Turns)-1].Model
}

func TestCodexModelOnALoadedThreadAppliesWithTheNextAgenthailTurn(t *testing.T) {
	fake, codex, session := pendingModelThread(t, nil, true)
	change, err := codex.SetModel(context.Background(), session, "gpt-sol")
	if err != nil {
		t.Fatal(err)
	}
	if change.Model != "gpt-luna" || change.PendingModel != "gpt-sol" || !strings.Contains(change.Text(), "gpt-sol applies from the next turn sent through Agenthail") || !strings.Contains(change.Text(), "Codex Desktop's composer") {
		t.Fatalf("change=%+v text=%q", change, change.Text())
	}
	if thread, _ := fake.Thread("thread"); thread.Model != "gpt-luna" {
		t.Fatalf("a loaded thread changed model on resume: %q", thread.Model)
	}
	if got := codex.PendingModel("thread"); got != "gpt-sol" {
		t.Fatalf("pending=%q", got)
	}
	if text, err := codex.Model(context.Background(), session, ""); err != nil || text != "gpt-luna" {
		t.Fatalf("read model=%q err=%v", text, err)
	}

	sent, err := codex.SendWithOptions(context.Background(), session, "hello", surface.SendOptions{})
	if err != nil || !sent.Accepted || sent.Notice != "" {
		t.Fatalf("sent=%+v err=%v", sent, err)
	}
	if param, ran := lastTurnModel(t, fake); param != "gpt-sol" || ran != "gpt-sol" {
		t.Fatalf("turn/start model=%v, turn ran on %q", param, ran)
	}
	if got := codex.PendingModel("thread"); got != "" {
		t.Fatalf("pending=%q after the turn that carried it", got)
	}

	fake.CompleteTurn("thread", "done")
	if _, err := codex.SendWithOptions(context.Background(), session, "again", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if param, ran := lastTurnModel(t, fake); param != nil || ran != "gpt-sol" {
		t.Fatalf("second turn/start model=%v, ran on %q", param, ran)
	}
}

func TestCodexModelOnAnUnloadedThreadAppliesNow(t *testing.T) {
	fake, codex, session := pendingModelThread(t, nil, false)
	change, err := codex.SetModel(context.Background(), session, "gpt-sol")
	if err != nil || change.Model != "gpt-sol" || change.PendingModel != "" || change.Text() != "gpt-sol" {
		t.Fatalf("change=%+v err=%v", change, err)
	}
	if thread, _ := fake.Thread("thread"); thread.Model != "gpt-sol" {
		t.Fatalf("thread model=%q", thread.Model)
	}
	if got := codex.PendingModel("thread"); got != "" {
		t.Fatalf("pending=%q", got)
	}
}

func TestCodexChoosingTheCurrentModelClearsThePendingOne(t *testing.T) {
	_, codex, session := pendingModelThread(t, nil, true)
	if _, err := codex.SetModel(context.Background(), session, "gpt-sol"); err != nil {
		t.Fatal(err)
	}
	change, err := codex.SetModel(context.Background(), session, "gpt-luna")
	if err != nil || change.PendingModel != "" || change.Model != "gpt-luna" {
		t.Fatalf("change=%+v err=%v", change, err)
	}
	if got := codex.PendingModel("thread"); got != "" {
		t.Fatalf("pending=%q", got)
	}
}

func TestCodexAnExplicitTurnModelReplacesThePendingOne(t *testing.T) {
	fake, codex, session := pendingModelThread(t, nil, true)
	if _, err := codex.SetModel(context.Background(), session, "gpt-sol"); err != nil {
		t.Fatal(err)
	}
	if _, err := codex.SendWithOptions(context.Background(), session, "hello", surface.SendOptions{Model: "gpt-terra"}); err != nil {
		t.Fatal(err)
	}
	if param, ran := lastTurnModel(t, fake); param != "gpt-terra" || ran != "gpt-terra" {
		t.Fatalf("turn/start model=%v, ran on %q", param, ran)
	}
	if got := codex.PendingModel("thread"); got != "" {
		t.Fatalf("pending=%q", got)
	}
}

func TestCodexAPendingModelIsDroppedWhenTheThreadMovedToAThirdModel(t *testing.T) {
	fake, codex, session := pendingModelThread(t, nil, true)
	if _, err := codex.SetModel(context.Background(), session, "gpt-sol"); err != nil {
		t.Fatal(err)
	}
	fake.SetThreadModel("thread", "gpt-terra")
	sent, err := codex.SendWithOptions(context.Background(), session, "hello", surface.SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if param, ran := lastTurnModel(t, fake); param != nil || ran != "gpt-terra" {
		t.Fatalf("turn/start model=%v, ran on %q; the newer choice was undone", param, ran)
	}
	if !strings.Contains(sent.Notice, "Dropped the pending model gpt-sol") || !strings.Contains(sent.Notice, "from gpt-luna to gpt-terra") {
		t.Fatalf("notice=%q", sent.Notice)
	}
	if got := codex.PendingModel("thread"); got != "" {
		t.Fatalf("pending=%q", got)
	}
}

func TestCodexAPendingModelThatAlreadyAppliesIsClearedWithoutBeingSent(t *testing.T) {
	fake, codex, session := pendingModelThread(t, nil, true)
	if _, err := codex.SetModel(context.Background(), session, "gpt-sol"); err != nil {
		t.Fatal(err)
	}
	fake.SetThreadModel("thread", "gpt-sol")
	sent, err := codex.SendWithOptions(context.Background(), session, "hello", surface.SendOptions{})
	if err != nil || sent.Notice != "" {
		t.Fatalf("sent=%+v err=%v", sent, err)
	}
	if param, _ := lastTurnModel(t, fake); param != nil {
		t.Fatalf("turn/start model=%v", param)
	}
	if got := codex.PendingModel("thread"); got != "" {
		t.Fatalf("pending=%q", got)
	}
}

func TestCodexAFailedTurnStartKeepsThePendingModel(t *testing.T) {
	fail := true
	fake, codex, session := pendingModelThread(t, func(method string, _ map[string]any) map[string]any {
		if method == "turn/start" && fail {
			return map[string]any{"error": map[string]any{"code": -32603, "message": "at capacity"}}
		}
		return nil
	}, true)
	if _, err := codex.SetModel(context.Background(), session, "gpt-sol"); err != nil {
		t.Fatal(err)
	}
	if _, err := codex.SendWithOptions(context.Background(), session, "hello", surface.SendOptions{}); err == nil {
		t.Fatal("send succeeded while turn/start failed")
	}
	if got := codex.PendingModel("thread"); got != "gpt-sol" {
		t.Fatalf("pending=%q after a failed turn/start", got)
	}
	fail = false
	if _, err := codex.SendWithOptions(context.Background(), session, "retry", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if param, ran := lastTurnModel(t, fake); param != "gpt-sol" || ran != "gpt-sol" {
		t.Fatalf("retry turn/start model=%v, ran on %q", param, ran)
	}
}

func TestCodexSessionsCarryThePendingModelAndItsFileIsWatched(t *testing.T) {
	_, codex, session := pendingModelThread(t, nil, true)
	if _, err := codex.SetModel(context.Background(), session, "gpt-sol"); err != nil {
		t.Fatal(err)
	}
	row := surface.Session{ID: "thread", Transcript: "/rollout.jsonl"}
	files := codex.LocalStatusFiles(row)
	if len(files) != 2 || files[0] != codexPendingModelPath("thread") {
		t.Fatalf("status files=%v", files)
	}
	fresh, err := codex.LocalStatus(context.Background(), surface.Session{ID: "thread"})
	if err != nil || fresh.PendingModel != "gpt-sol" {
		t.Fatalf("a thread without a rollout: pending=%q err=%v", fresh.PendingModel, err)
	}
	if files := codex.LocalStatusFiles(surface.Session{ID: "thread"}); len(files) != 1 || files[0] != codexPendingModelPath("thread") {
		t.Fatalf("status files without a rollout=%v", files)
	}
	applyCodexPendingModel(&row)
	if row.PendingModel != "gpt-sol" {
		t.Fatalf("row pending=%q", row.PendingModel)
	}
	if _, err := codex.SetModel(context.Background(), session, "gpt-luna"); err != nil {
		t.Fatal(err)
	}
	applyCodexPendingModel(&row)
	if row.PendingModel != "" {
		t.Fatalf("row pending=%q after choosing the current model", row.PendingModel)
	}
}

func TestCodexPendingModelPathRejectsIDsThatAreNotFileNames(t *testing.T) {
	for _, id := range []string{"", "../escape", "a/b", ".hidden"} {
		if path := codexPendingModelPath(id); path != "" {
			t.Fatalf("id %q gave path %q", id, path)
		}
	}
}

func TestCodexADroppedPendingModelIsReportedByTheTurnThatStarts(t *testing.T) {
	fail := true
	fake, codex, session := pendingModelThread(t, func(method string, _ map[string]any) map[string]any {
		if method == "turn/start" && fail {
			return map[string]any{"error": map[string]any{"code": -32603, "message": "at capacity"}}
		}
		return nil
	}, true)
	if _, err := codex.SetModel(context.Background(), session, "gpt-sol"); err != nil {
		t.Fatal(err)
	}
	fake.SetThreadModel("thread", "gpt-terra")
	if _, err := codex.SendWithOptions(context.Background(), session, "hello", surface.SendOptions{}); err == nil {
		t.Fatal("send succeeded while turn/start failed")
	}
	fail = false
	sent, err := codex.SendWithOptions(context.Background(), session, "retry", surface.SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sent.Notice, "Dropped the pending model gpt-sol") {
		t.Fatalf("notice=%q", sent.Notice)
	}
	if param, ran := lastTurnModel(t, fake); param != nil || ran != "gpt-terra" {
		t.Fatalf("turn/start model=%v, ran on %q", param, ran)
	}
	if got := codex.PendingModel("thread"); got != "" {
		t.Fatalf("pending=%q", got)
	}
}
