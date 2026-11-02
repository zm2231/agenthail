package ledger_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/ledger"
)

func seedUncapped(t *testing.T, db *sql.DB, key, typ, ref string, payload any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO ledger_events(project_key,type,payload,actor_session_id,ref,created_at) VALUES(?,?,?,?,?,?)`, key, typ, data, "s1", ref, time.Now().UTC().Format("2006-01-02T15:04:05Z")); err != nil {
		t.Fatal(err)
	}
}

func TestAnOverCapEventKeepsTheProjectLocalOnEitherSide(t *testing.T) {
	words := ledger.UtterancePayload{Text: strings.Repeat("x", ledger.MaxEventBytes), UtteranceID: "claude-prompt:old", SessionID: "s1", CapturedBy: ledger.CapturedByHook}

	mac, owner := newSyncHost(t), newSyncHost(t)
	seedUncapped(t, mac.db, project, string(ledger.EventUtterance), "claude-prompt:old", words)
	if err := owner.store.ClaimOwnership(project, mac.id, true); err != nil {
		t.Fatal(err)
	}
	if err := mac.store.AssignOwner(project, owner.id); err == nil || !strings.Contains(err.Error(), "cannot sync") || !strings.Contains(err.Error(), "(utterance)") {
		t.Fatalf("an over-cap event was handed to replay: %v", err)
	}
	if remote, _, err := mac.store.Owner(project); err != nil || remote != "" {
		t.Fatalf("the project left local after a refused assignment: owner %q, %v", remote, err)
	}

	const other = "repo:github.com/acme/infra"
	seedUncapped(t, owner.db, other, "goal", "old-goal", map[string]string{"goalId": "infra-G1", "name": strings.Repeat("n", ledger.MaxEventBytes)})
	if err := owner.store.ClaimOwnership(other, mac.id, false); err == nil || !strings.Contains(err.Error(), "cannot sync") {
		t.Fatalf("an owner with an over-cap event took ownership: %v", err)
	}
	if remote, _, err := owner.store.Owner(other); err != nil || remote != "" {
		t.Fatalf("the owner's project left local after a refused claim: owner %q, %v", remote, err)
	}
}

func TestOversizedWordsWithControlAndInvalidBytesKeepAValidHead(t *testing.T) {
	s := newSyncHost(t).store
	text := strings.Repeat("\x01\xff<é", ledger.MaxEventBytes/3)
	capture(t, s, project, "claude-prompt:raw", text)
	for _, u := range board(t, s).Utterances() {
		if u.ID == "claude-prompt:raw" {
			if u.Truncated == nil || u.Truncated.OriginalBytes != len(text) || !utf8.ValidString(u.Text) {
				t.Fatalf("raw bytes were not kept as a marked head: %+v", u.Truncated)
			}
			return
		}
	}
	t.Fatal("the words were not recorded")
}
