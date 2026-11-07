package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func probedHarness(t *testing.T, allow func(map[string]bool)) (*harness, *bytes.Buffer) {
	t.Helper()
	h := newTestHarness(t)
	var out bytes.Buffer
	roots := h.watch.roots
	h.watch = newWatcherTo(&out, newBackend(), 50*time.Millisecond, roots)
	allowed := map[string]bool{}
	for path := range h.executables {
		allowed[path] = true
	}
	allow(allowed)
	h.executables = allowed
	if err := h.start(context.Background(), "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	h.shutdown()
	return h, &out
}

func TestEveryToolOnAHostsPathRunsAsAProgramTheWatcherAllows(t *testing.T) {
	h, out := probedHarness(t, func(map[string]bool) {})
	if v := h.watch.violations(); len(v) != 0 {
		t.Fatalf("a tool on the host's PATH broke isolation: %q\n%s", v, out)
	}
	if blind := h.watch.blind(); len(blind) != 0 {
		t.Fatalf("the watcher missed a probe: %q\n%s", blind, out)
	}
	for _, tool := range []string{"git", "sleep"} {
		if !strings.Contains(out.String(), "/"+tool+"\"") {
			t.Errorf("no program line for %s:\n%s", tool, out)
		}
	}
}

func TestAToolTheAllowListMissesIsReported(t *testing.T) {
	h, out := probedHarness(t, func(allowed map[string]bool) {
		for path := range allowed {
			if strings.HasSuffix(path, "/git") {
				delete(allowed, path)
			}
		}
	})
	found := strings.Join(h.watch.violations(), "\n")
	if !strings.Contains(found, "runs a program the harness did not put on its PATH") || !strings.Contains(found, "git") {
		t.Fatalf("git outside the allow list was not reported:\n%s\n%s", found, out)
	}
}
