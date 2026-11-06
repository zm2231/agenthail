package registry

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestARealRegistryCopyKeepsEveryPromptThroughTheRoleMigration(t *testing.T) {
	source := os.Getenv("AGENTHAIL_REGISTRY_COPY")
	if source == "" {
		t.Skip("set AGENTHAIL_REGISTRY_COPY to a scrubbed registry copy")
	}
	path := filepath.Join(t.TempDir(), "registry.db")
	copyRegistryFile(t, source, path)
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.migration == nil {
		t.Fatal("the copy was already at the current schema")
	}
	t.Logf("migrated %d to %d in %v (backup %v)", r.migration.From, r.migration.To, r.migration.Migrate, r.migration.Backup)
	var stored, parsed, mismatched int
	if err := r.db.QueryRow(`SELECT
		(SELECT count(*) FROM session_journal WHERE payload_role='user'),
		(SELECT count(*) FROM session_journal WHERE kind IN ('message','text') AND payload_codec=0 AND json_valid(payload) AND json_extract(payload,'$.role')='user'),
		(SELECT count(*) FROM session_journal WHERE payload_role='user' AND (kind NOT IN ('message','text') OR json_extract(payload,'$.role') IS NOT 'user'))`).Scan(&stored, &parsed, &mismatched); err != nil {
		t.Fatal(err)
	}
	if stored != parsed || mismatched != 0 || stored == 0 {
		t.Fatalf("payload_role marks %d rows user, the payloads hold %d, %d marked rows disagree", stored, parsed, mismatched)
	}
	began := time.Now()
	prompts, err := r.JournalPrompts(JournalPromptQuery{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d user rows; newest %d prompts read in %v", stored, len(prompts), time.Since(began))
}

func copyRegistryFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
