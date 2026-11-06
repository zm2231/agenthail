package registry

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const queueCountHost = "h0123456789ab"

func applyMirror(t *testing.T, r *Registry, id string, hostCount int) {
	t.Helper()
	entity := queueCountHost + "::" + id
	row := HostCatalogRow{Session: surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Name: id}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`), QueueCount: hostCount}
	if _, _, err := r.ApplyHostCatalogRow(queueCountHost, row); err != nil {
		t.Fatal(err)
	}
}

// Every mirrored row's projected count must equal its host's count plus the
// sends held here, as a full recompute of every row would make it.
func assertMirroredCounts(t *testing.T, r *Registry, step string) {
	t.Helper()
	rows, err := r.db.Query(`SELECT cs.session_id,json_extract(cs.projection_fingerprint,'$.queueCount'),
		cs.host_queue_count+(SELECT COUNT(*) FROM message_queue q WHERE q.session_id=cs.session_id AND q.status IN ('pending','inflight'))
		FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id WHERE s.host_id<>''`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var projected *int
		var want int
		if err := rows.Scan(&id, &projected, &want); err != nil {
			t.Fatal(err)
		}
		if projected == nil {
			t.Fatalf("%s: %s projects no queue count, a full recompute gives %d", step, id, want)
		}
		if *projected != want {
			t.Fatalf("%s: %s projects %d queued, a full recompute gives %d", step, id, *projected, want)
		}
	}
}

func TestMirroredQueueCountsMatchAFullRecomputeAfterAnyQueueChange(t *testing.T) {
	r := openTestRegistry(t)
	if _, err := r.AddHost(PairedHost{ID: queueCountHost, Name: "mac", Endpoint: "https://mac.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	mirrored := []string{"m0", "m1", "m2", "m3"}
	for i, id := range mirrored {
		applyMirror(t, r, id, i)
	}
	locals := []string{"l0", "l1", "l2"}
	for _, id := range locals {
		session := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle}
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: `{"id":"` + id + `","queueCount":0}`}, CatalogEvent{DedupeKey: id + ":upsert", Type: "session.upserted", EntityID: id, Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.RefreshHostQueueCounts(); err != nil {
		t.Fatal(err)
	}
	assertMirroredCounts(t, r, "start")

	random := rand.New(rand.NewSource(67))
	statuses := []string{"pending", "inflight", "delivered", "canceled", "dead"}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := r.db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	queued := func() []int64 {
		var ids []int64
		rows, err := r.db.Query(`SELECT id FROM message_queue ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			ids = append(ids, id)
		}
		return ids
	}
	sessions := func() []string { return append(append([]string{}, mirrored...), locals...) }
	for step := 0; step < 400; step++ {
		ids := queued()
		all := sessions()
		switch op := random.Intn(7); {
		case op == 0 || len(ids) == 0:
			target := all[random.Intn(len(all))]
			if random.Intn(2) == 0 {
				if _, err := r.QueueHostDelivery(target, fmt.Sprintf("send %d", step), "", []byte(`{}`), false); err != nil {
					exec(`INSERT INTO message_queue(session_id,message,status) VALUES(?,?,'pending')`, target, fmt.Sprintf("send %d", step))
				}
			} else {
				exec(`INSERT INTO message_queue(session_id,message,status) VALUES(?,?,?)`, target, fmt.Sprintf("send %d", step), statuses[random.Intn(len(statuses))])
			}
		case op == 1 || op == 2:
			exec(`UPDATE message_queue SET status=? WHERE id=?`, statuses[random.Intn(len(statuses))], ids[random.Intn(len(ids))])
		case op == 3:
			exec(`UPDATE message_queue SET session_id=? WHERE id=?`, all[random.Intn(len(all))], ids[random.Intn(len(ids))])
		case op == 4:
			exec(`DELETE FROM message_queue WHERE id=?`, ids[random.Intn(len(ids))])
		case op == 5:
			applyMirror(t, r, mirrored[random.Intn(len(mirrored))], random.Intn(4))
		case op == 6 && len(locals) > 0 && random.Intn(4) == 0:
			id := locals[0]
			locals = locals[1:]
			exec(`UPDATE sessions SET host_id=? WHERE id=?`, queueCountHost, id)
			mirrored = append(mirrored, id)
		}
		if random.Intn(3) == 0 {
			if _, err := r.RefreshHostQueueCounts(); err != nil {
				t.Fatal(err)
			}
			assertMirroredCounts(t, r, fmt.Sprintf("step %d", step))
		}
	}
	if len(locals) != 0 {
		t.Fatalf("the run never mirrored %v", locals)
	}
	var moved int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM message_queue WHERE session_id IN ('m0','m1','m2','m3')`).Scan(&moved); err != nil || moved == 0 {
		t.Fatalf("no queue rows reached mirrored sessions: %d %v", moved, err)
	}
}

func TestARefreshWithNoQueueChangeTakesNoWriteLock(t *testing.T) {
	r := openTestRegistry(t)
	if _, err := r.AddHost(PairedHost{ID: queueCountHost, Name: "mac", Endpoint: "https://mac.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	applyMirror(t, r, "m0", 1)
	if _, err := r.RefreshHostQueueCounts(); err != nil {
		t.Fatal(err)
	}
	hold, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback()
	if _, err := hold.Exec(`UPDATE hosts SET name=name WHERE host_id=?`, queueCountHost); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.RefreshHostQueueCounts()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a refresh with nothing to do waited on another writer")
	}
}
