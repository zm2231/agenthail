package systemone_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/systemone"
)

func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".agenthail")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAUserIsOffUntilConfiguredAndRefusesAMalformedKey(t *testing.T) {
	home := t.TempDir()
	if _, ok, err := systemone.LoadConfig(home, "ledgerSorter"); ok || err != nil {
		t.Fatalf("no config file: ok=%v err=%v", ok, err)
	}
	writeConfig(t, home, `{"other": true}`)
	if _, ok, err := systemone.LoadConfig(home, "ledgerSorter"); ok || err != nil {
		t.Fatalf("no key: ok=%v err=%v", ok, err)
	}
	writeConfig(t, home, `{"ledgerSorter": {"url": " http://127.0.0.1:8100 ", "model": "d1-3b"}}`)
	if config, ok, err := systemone.LoadConfig(home, "ledgerSorter"); !ok || err != nil || config != (systemone.Config{URL: "http://127.0.0.1:8100", Model: "d1-3b"}) {
		t.Fatalf("configured: %+v ok=%v err=%v", config, ok, err)
	}
	for _, bad := range []string{`{"ledgerSorter": "on"}`, `{"ledgerSorter": {"url": "127.0.0.1:8100", "model": "d1-3b"}}`, `{"ledgerSorter": {"url": "http://127.0.0.1:8100"}}`} {
		writeConfig(t, home, bad)
		if _, ok, err := systemone.LoadConfig(home, "ledgerSorter"); ok || err == nil || !strings.Contains(err.Error(), "ledgerSorter must be") {
			t.Fatalf("%s: ok=%v err=%v", bad, ok, err)
		}
	}
}
