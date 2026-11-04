package daemon

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
)

func TestABrokenSettingsFileDoesNotStopTheQueue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d, r, _, _, child := daemonFixture(t)
	var logged bytes.Buffer
	d.log = log.New(&logged, "", 0)
	path := filepath.Join(home, ".agenthail", "dashboard.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(brokenSettings), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(child.ID, "still delivered"); err != nil {
		t.Fatal(err)
	}
	rows, err := r.ListQueue(false, nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if _, err := r.PendingSteer(child.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := r.ClaimNextMessage(child.ID, time.Now())
	if err != nil || claimed == nil || claimed.Message != "still delivered" {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	if r.Steers("") {
		t.Fatal("a broken file steered")
	}
	if count := strings.Count(logged.String(), "busy delivery setting unreadable"); count != 1 {
		t.Fatalf("logged %d times: %s", count, logged.String())
	}
	if !strings.Contains(logged.String(), DashboardConfigPath()) {
		t.Fatalf("log did not name the file: %s", logged.String())
	}
	if err := os.WriteFile(path, []byte(`{"busyDelivery":"steer"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if d.busyPolicy() != deliverypolicy.Steer || !strings.Contains(logged.String(), "readable again") {
		t.Fatalf("after the fix: %s", logged.String())
	}
}
