package surface

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAStampReadWithinOneTickOfAChangeMatchesNoOtherStamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	modified := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, readAt := range []time.Time{modified, modified.Add(MtimeGranularity), modified.Add(-time.Minute)} {
		if first, second := SettledStamp(info, readAt), SettledStamp(info, readAt); first == second {
			t.Fatalf("read %s after the change: two reads at the same instant share stamp %q", readAt.Sub(modified), first)
		}
	}
	settled := modified.Add(MtimeGranularity + time.Nanosecond)
	if first, second := SettledStamp(info, settled), SettledStamp(info, settled.Add(time.Hour)); first != second {
		t.Fatalf("settled reads disagree: %q and %q", first, second)
	}
}
