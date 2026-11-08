package daemon

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry/registrytest"
)

// Apps resend unconfirmed messages under their saved keys after any delay.
func TestActionIdempotencyOldReceiptReplaysAfterRestartWithoutDispatch(t *testing.T) {
	path := registrytest.Path(t)
	fake := idempotencyFake()
	r, handler := openIdempotencyFixture(t, path, fake)
	token := pairedControlToken(t, r)
	const body = `{"action":"send","sessionId":"from","message":"sent before the app quit"}`
	first := serveV1Action(handler, body, "saved-key", token)
	if first.Code != http.StatusOK || len(fake.sent) != 1 {
		t.Fatalf("first status=%d sends=%d body=%s", first.Code, len(fake.sent), first.Body.String())
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(-1, 0, 0).UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE action_receipts SET created_at=?, updated_at=?`, old, old); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	restartedFake := idempotencyFake()
	_, restarted := openIdempotencyFixture(t, path, restartedFake)
	replay := serveV1Action(restarted, body, "saved-key", token)
	if replay.Code != first.Code || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay status=%d body=%s, want %d %s", replay.Code, replay.Body.String(), first.Code, first.Body.String())
	}
	if len(restartedFake.sent) != 0 {
		t.Fatalf("a year-old key re-dispatched after restart: sends=%d", len(restartedFake.sent))
	}
}
