package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTwoCodexAdaptersInOneProcessKeepToTheirOwnHomes(t *testing.T) {
	now := time.Date(2026, 10, 23, 14, 0, 0, 0, time.UTC)
	homes := map[float64]CodexHome{}
	adapters := map[float64]*Codex{}
	for _, used := range []float64{10, 70} {
		home := CodexHome{Codex: t.TempDir(), State: t.TempDir()}
		writeRollout(t, home.Codex, "2026/10/23", "rollout.jsonl", now,
			tokenCount("2026-10-23T13:00:00Z", `{"primary":{"used_percent":`+strconv.FormatFloat(used, 'f', -1, 64)+`,"window_minutes":10080,"resets_at":1793293773},"secondary":null}`))
		homes[used] = home
		adapters[used] = NewCodexIn(home, "")
	}
	elsewhere := t.TempDir()
	t.Setenv("HOME", elsewhere)
	t.Setenv("CODEX_HOME", filepath.Join(elsewhere, ".codex"))

	for used, codex := range adapters {
		usage, err := codex.Usage(context.Background())
		if err != nil || len(usage.Limits) != 1 || usage.Limits[0].Percent != used {
			t.Fatalf("the adapter on %s read usage %+v (%v), want %v%%", homes[used].Codex, usage, err, used)
		}
		if dirs := codex.CatalogWatchDirs(); len(dirs) == 0 || !strings.HasPrefix(dirs[0], homes[used].Codex) {
			t.Fatalf("the adapter on %s watches %v", homes[used].Codex, dirs)
		}
	}

	held, err := homes[10].acquireWriteLock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseCodexWriteLock(held)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	other, err := homes[70].acquireWriteLock(ctx)
	if err != nil {
		t.Fatalf("one adapter's write lock held the other's: %v", err)
	}
	releaseCodexWriteLock(other)
	if _, err := homes[10].acquireWriteLock(ctx); err == nil {
		t.Fatal("an adapter took its own write lock twice")
	}

	env := homes[70].ChildEnv([]string{"CODEX_HOME=" + elsewhere, "PATH=/usr/bin"})
	if !slices.Equal(env, []string{"PATH=/usr/bin", "CODEX_HOME=" + homes[70].Codex}) {
		t.Fatalf("a Codex child of the adapter on %s gets %v", homes[70].Codex, env)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Fatalf("an adapter wrote into the process's current home: %v %v", entries, err)
	}
}
