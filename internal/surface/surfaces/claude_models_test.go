package surfaces

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestClaudeModelsCoalescesAndCachesSuccessfulCatalog(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "claude")
	script := `#!/bin/sh
IFS= read -r _
printf x >> "$HOME/catalog-calls"
sleep 0.05
printf '%s\n' '{"type":"control_response","response":{"request_id":"agenthail-model-catalog","response":{"models":[{"value":"haiku","displayName":"Haiku"}]}}}'
sleep 30
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	claude := NewClaude("Default", home)
	var group sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			models, err := claude.Models(context.Background())
			if err == nil && (len(models) != 1 || models[0].ID != "haiku") {
				err = fmt.Errorf("models=%+v", models)
			}
			errs <- err
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := claude.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(filepath.Join(home, "catalog-calls"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "x") != 1 {
		t.Fatalf("catalog subprocesses=%d", strings.Count(string(calls), "x"))
	}
}

func TestClaudeModelCatalogErrorIsNotCached(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "claude")
	script := `#!/bin/sh
IFS= read -r _
count=$(wc -c < "$HOME/catalog-calls" 2>/dev/null || echo 0)
printf x >> "$HOME/catalog-calls"
if [ "$count" -eq 0 ]; then
  printf '%s\n' '{"type":"control_response","response":{"request_id":"agenthail-model-catalog","response":{"models":[]}}}'
else
  printf '%s\n' '{"type":"control_response","response":{"request_id":"agenthail-model-catalog","response":{"models":[{"value":"haiku","displayName":"Haiku"}]}}}'
fi
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	claude := NewClaude("Default", home)
	if _, err := claude.Models(context.Background()); err == nil {
		t.Fatal("empty catalog was accepted")
	}
	models, err := claude.Models(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "haiku" {
		t.Fatalf("second catalog=%+v err=%v", models, err)
	}
}
