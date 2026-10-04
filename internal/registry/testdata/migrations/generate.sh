#!/bin/sh
# Each dump is written by the listed commit's own registry package.
set -eu

repo=$(git rev-parse --show-toplevel)
out="$repo/internal/registry/testdata/migrations"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

queue_helpers='
func deliver(t *testing.T, r *Registry, message string) {
	must(t, r.QueueMessage("target", message))
	item, err := r.ClaimNextMessage("target", time.Now())
	must(t, err)
	must(t, r.AckMessage(item.ID))
}

func pinQueueClock(t *testing.T, r *Registry) {
	_, err := r.db.Exec(`UPDATE message_queue SET queued_at=?, expires_at_ms=CASE WHEN expires_at_ms>0 THEN ? ELSE 0 END`, "2026-09-01 00:00:00", time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC).UnixMilli())
	must(t, err)
}'

fixture() {
	name=$1 commit=$2 version=$3 body=$4 helpers=${5-$queue_helpers}
	tree="$work/$name"
	mkdir -p "$tree"
	git -C "$repo" archive "$commit" | tar -x -C "$tree"
	cat >"$tree/internal/registry/zz_fixture_test.go" <<EOF
package registry

import (
	"os"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var _ = time.Now

func TestWriteMigrationFixture(t *testing.T) {
	r, err := Open(os.Getenv("FIXTURE_OUT"))
	must(t, err)
	defer r.Close()
	for _, s := range []surface.Session{
		{ID: "sender", Surface: surface.KindCodex, Name: "Sender"},
		{ID: "target", Surface: surface.KindCodex, Name: "Target", Cwd: "/work/target"},
	} {
		must(t, r.RegisterSession(s))
	}
	must(t, r.SetAlias("builder", "target"))
	_, err = r.AddRoute("sender", "target", ".*")
	must(t, err)
$body
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
$helpers
EOF
	(cd "$tree" && GOFLAGS=-mod=mod FIXTURE_OUT="$work/$name.db" go test -count=1 -run '^TestWriteMigrationFixture$' ./internal/registry >/dev/null)
	{
		printf -- '-- Registry database written by internal/registry at %s (%s).\n' "$commit" "$(git -C "$repo" log -1 --format=%s "$commit")"
		printf -- '-- Regenerate with generate.sh in this directory.\n'
		sqlite3 "$work/$name.db" .dump
		printf 'PRAGMA user_version=%s;\n' "$version"
	} >"$out/$name.sql"
}

fixture v0 86dce76 0 '
	must(t, r.QueueMessage("target", "delivered hello"))
	must(t, r.QueueMessage("target", "pending hello"))
	_, err = r.db.Exec(`UPDATE message_queue SET delivered=1 WHERE message=?`, "delivered hello")
	must(t, err)
	_, err = r.db.Exec(`UPDATE message_queue SET queued_at=?`, "2026-09-01 00:00:00")
	must(t, err)' ''

fixture v1 f0fdeaa 1 '
	deliver(t, r, "delivered hello")
	must(t, r.QueueMessage("target", "pending hello"))
	_, err = r.db.Exec(`INSERT INTO message_queue(session_id,message,status,delivered,expires_at_ms) VALUES(?,?,?,?,0)`, "target", "legacy blank status", "", 1)
	must(t, err)
	pinQueueClock(t, r)'

for spec in v2:6b1c51c:2 v3:53300a6:3 v4:713a3ea:4 v5:a2cbb88:5 v7:de2fda1:7 v9:fe38849:9; do
	name=${spec%%:*} rest=${spec#*:}
	fixture "$name" "${rest%%:*}" "${rest#*:}" '
	deliver(t, r, "delivered hello")
	_, err = r.QueueMessageWithOptions("target", "pending hello", "", surface.SendOptions{SourceSessionID: "sender"})
	must(t, err)
	pinQueueClock(t, r)'
done

fixture v8 be05321 8 '
	deliver(t, r, "delivered hello")
	_, err = r.QueueMessageWithOptions("target", "pending hello", "", surface.SendOptions{SourceSessionID: "sender"})
	must(t, err)
	pinQueueClock(t, r)
	intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Status: DeliveryIntentQueued, Evidence: surface.EvidenceQueued})
	must(t, err)
	_, err = r.FailDeliveryIntent(intent.ID, DeliveryIntentFailed, "transport failed")
	must(t, err)
	_, _, err = r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "target", Kind: "item", ProviderKey: "body", Payload: []byte("p"), BodyRef: "body-ref", FullBody: []byte("123456789")}, SessionJournalRetention{Count: 4, Bytes: 10})
	must(t, err)'

fixture v10 fc8957e 10 '
	deliver(t, r, "delivered hello")
	_, err = r.QueueMessageWithOptions("target", "pending hello", "", surface.SendOptions{SourceSessionID: "sender"})
	must(t, err)
	pinQueueClock(t, r)
	_, _, err = r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "target", Kind: "text", ProviderKey: "timeline:old", Payload: []byte(`{"itemId":"old"}`)}, SessionJournalRetention{Count: 10, Bytes: 1024})
	must(t, err)'

fixture v11 1d64825 11 '
	deliver(t, r, "delivered hello")
	_, err = r.QueueMessageWithOptions("target", "pending hello", "", surface.SendOptions{SourceSessionID: "sender"})
	must(t, err)
	pinQueueClock(t, r)
	_, _, err = r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "target", Kind: "text", ProviderKey: "timeline:old", Payload: []byte(`{"itemId":"old"}`)}, SessionJournalRetention{Count: 10, Bytes: 1024})
	must(t, err)
	must(t, r.MarkSessionJournalSeed("target", true))'
