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
	for _, content := range []string{`{"busyDelivery":"drop"}`, `{"busyDelivery":7}`, `{not json`} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		mode, err := LoadFromPath(path)
		if err == nil || mode != Queue {
			t.Fatalf("%s: mode=%q err=%v", content, mode, err)
		}
	}
}

func TestAnotherBadFieldDoesNotAffectTheSetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dashboard.json")
	if err := os.WriteFile(path, []byte(`{"busyDelivery":"steer","listen":7}`), 0600); err != nil {
		t.Fatal(err)
	}
	if mode, err := LoadFromPath(path); err != nil || mode != Steer {
		t.Fatalf("mode=%q err=%v", mode, err)
	}
}
