package deliverypolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromPathDefaultsAndValidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dashboard.json")
	mode, err := LoadFromPath(path)
	if err != nil || mode != Queue {
		t.Fatalf("mode=%q err=%v", mode, err)
	}
	if err := os.WriteFile(path, []byte(`{"busyDelivery":"steer"}`), 0600); err != nil {
		t.Fatal(err)
	}
	mode, err = LoadFromPath(path)
	if err != nil || mode != Steer {
		t.Fatalf("mode=%q err=%v", mode, err)
	}
	if err := os.WriteFile(path, []byte(`{"busyDelivery":"drop"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFromPath(path); err == nil {
		t.Fatal("invalid mode accepted")
	}
}
