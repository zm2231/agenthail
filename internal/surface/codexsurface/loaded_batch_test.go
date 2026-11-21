package codexsurface

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
)

func loadedThreads(t *testing.T, respond codextest.Responder) (*codextest.Daemon, []string) {
	t.Helper()
	daemon := startManagedCodex(t, respond)
	var ids []string
	for index := 1; index <= 3; index++ {
		id := fmt.Sprintf("019f004a-0000-7000-8000-0000000000c%d", index)
		daemon.AddThread(codextest.Thread{ID: id, Cwd: t.TempDir()})
		daemon.Load(id)
		ids = append(ids, id)
	}
	return daemon, ids
}

func listedIDs(t *testing.T) []string {
	t.Helper()
	sessions, err := isolatedManagedRuntime(t).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, session := range sessions {
		ids = append(ids, session.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestCodexReadsLoadedThreadsInOneListing(t *testing.T) {
	daemon, ids := loadedThreads(t, nil)
	if got := listedIDs(t); fmt.Sprint(got) != fmt.Sprint(ids) {
		t.Fatalf("listed %v want %v", got, ids)
	}
	if reads := len(daemon.Calls("thread/read")); reads != 0 {
		t.Fatalf("loaded threads were read one by one: %d thread/read calls", reads)
	}
}

func TestCodexWithoutIncludedThreadIdsStillListsLoadedThreads(t *testing.T) {
	daemon, ids := loadedThreads(t, func(method string, params map[string]any) map[string]any {
		if _, targeted := params["includedThreadIds"]; method == "thread/list" && targeted {
			return map[string]any{"data": []any{map[string]any{"id": "019f004a-0000-7000-8000-0000000000ff", "source": "vscode", "status": map[string]any{"type": "notLoaded"}}}}
		}
		return nil
	})
	if got := listedIDs(t); fmt.Sprint(got) != fmt.Sprint(ids) {
		t.Fatalf("listed %v want %v", got, ids)
	}
	if reads := len(daemon.Calls("thread/read")); reads != len(ids) {
		t.Fatalf("thread/read calls=%d want %d", reads, len(ids))
	}
}
