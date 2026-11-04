package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/uploads"
)

const pastDeadUploadRetention = 31 * 24 * time.Hour

type queuedUploadView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Missing   bool   `json:"missing"`
}

type queuedImage struct {
	daemon   *Daemon
	adapter  *imageSurface
	handler  http.Handler
	uploadID string
	queueID  int64
}

func queueImageWhileBusy(t *testing.T) queuedImage {
	t.Helper()
	d, adapter, handler := uploadFixture(t)
	picture := decodeUpload(t, postUpload(t, handler, "to", "shot.png", "image/png", testPNG(t, 4, 4)))
	adapter.accepted = false
	response := postAction(t, handler, map[string]any{"action": "send", "sessionId": "to", "message": "look at this", "author": "person", "busyDelivery": "steer", "attachments": []string{picture.Upload.ID}})
	var answer struct {
		Result struct {
			Evidence string `json:"evidence"`
			QueueID  int64  `json:"queueId"`
			Detail   string `json:"detail"`
		} `json:"result"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil || answer.Result.Evidence != "queued" || answer.Result.QueueID == 0 {
		t.Fatalf("busy send status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(answer.Result.Detail, "attached") {
		t.Errorf("the receipt does not say the attachment is why it waits: %q", answer.Result.Detail)
	}
	return queuedImage{daemon: d, adapter: adapter, handler: handler, uploadID: picture.Upload.ID, queueID: answer.Result.QueueID}
}

func (q queuedImage) ageUploadAndSweep(t *testing.T) {
	t.Helper()
	digest, _, _ := strings.Cut(strings.TrimPrefix(q.uploadID, "upload:"), "/")
	old := time.Now().Add(-uploads.Retention - time.Hour)
	if err := os.Chtimes(filepath.Join(uploadsDir(q.daemon.Registry), digest), old, old); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q.daemon.sweepUploads(ctx)
}

func (q queuedImage) drain(t *testing.T) {
	t.Helper()
	q.adapter.accepted = true
	session, err := q.daemon.Registry.Session("to")
	if err != nil {
		t.Fatal(err)
	}
	q.daemon.drainMessageQueue(context.Background(), q.adapter, session)
}

func TestAQueuedImageOutlivesTheUploadRetention(t *testing.T) {
	q := queueImageWhileBusy(t)
	q.ageUploadAndSweep(t)
	q.drain(t)
	if len(q.adapter.images) != 1 || len(q.adapter.images[0]) != 1 {
		t.Fatalf("drained images=%+v", q.adapter.images)
	}
	if _, err := os.Stat(q.adapter.images[0][0].Path); err != nil {
		t.Fatalf("the runtime was handed a swept file: %v", err)
	}
}

func TestRetryingAnOldDeadImageMessageFailsVisiblyWhenItsFileIsGone(t *testing.T) {
	q := queueImageWhileBusy(t)
	longAgo := time.Now().Add(-pastDeadUploadRetention).UnixMilli()
	if _, err := q.daemon.Registry.WriteDB().Exec(`UPDATE message_queue SET status='dead',last_error='synthetic failure',dead_at_ms=? WHERE id=?`, longAgo, q.queueID); err != nil {
		t.Fatal(err)
	}
	q.ageUploadAndSweep(t)
	if response := postAction(t, q.handler, map[string]any{"action": "queue-retry", "queueId": q.queueID}); response.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", response.Code, response.Body.String())
	}
	sent := len(q.adapter.sent)
	q.drain(t)
	if len(q.adapter.images) != 0 || len(q.adapter.sent) != sent {
		t.Fatalf("a message whose file is gone reached the runtime: images=%+v sent=%q", q.adapter.images, q.adapter.sent)
	}
	var status, lastError string
	if err := q.daemon.Registry.ReadDB().QueryRow(`SELECT status,last_error FROM message_queue WHERE id=?`, q.queueID).Scan(&status, &lastError); err != nil {
		t.Fatal(err)
	}
	if status != "dead" || !strings.Contains(lastError, "shot.png") || !strings.Contains(lastError, "attach it again") {
		t.Fatalf("status=%q last_error=%q", status, lastError)
	}
}

func TestAQueuedMessageShowsTheImagesItCarries(t *testing.T) {
	q := queueImageWhileBusy(t)
	uploadID, queueID := q.uploadID, q.queueID
	request := httptest.NewRequest(http.MethodGet, "/api/v1/queue?sessionId=to", nil)
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	response := httptest.NewRecorder()
	q.handler.ServeHTTP(response, request)
	var page struct {
		Items []struct {
			ID          int64              `json:"id"`
			Attachments []queuedUploadView `json:"attachments"`
		} `json:"items"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatalf("queue status=%d body=%s", response.Code, response.Body.String())
	}
	for _, item := range page.Items {
		if item.ID == queueID {
			if len(item.Attachments) != 1 || item.Attachments[0].ID != uploadID || item.Attachments[0].MediaType != "image/png" {
				t.Fatalf("queued item attachments=%+v", item.Attachments)
			}
			return
		}
	}
	t.Fatalf("queued item %d missing from %s", queueID, response.Body.String())
}

func TestCancelingAQueuedMessageHandsBackItsTextAndImagesForAFullRetention(t *testing.T) {
	q := queueImageWhileBusy(t)
	q.ageUploadAndSweep(t)
	response := postAction(t, q.handler, map[string]any{"action": "queue-cancel", "queueId": q.queueID})
	var answer struct {
		Result struct {
			Message     string             `json:"message"`
			Attachments []queuedUploadView `json:"attachments"`
		} `json:"result"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil {
		t.Fatalf("cancel status=%d body=%s", response.Code, response.Body.String())
	}
	if answer.Result.Message != "look at this" || len(answer.Result.Attachments) != 1 || answer.Result.Attachments[0].ID != q.uploadID || answer.Result.Attachments[0].Missing {
		t.Fatalf("canceled item came back as %s", response.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q.daemon.sweepUploads(ctx)
	q.adapter.accepted = true
	resend := postAction(t, q.handler, map[string]any{"action": "send", "sessionId": "to", "message": answer.Result.Message, "author": "person", "attachments": []string{answer.Result.Attachments[0].ID}})
	if resend.Code != http.StatusOK || len(q.adapter.images) != 1 || len(q.adapter.images[0]) != 1 {
		t.Fatalf("resend of the handed-back image status=%d body=%s images=%+v", resend.Code, resend.Body.String(), q.adapter.images)
	}
}

func TestCancelingAQueuedMessageWhoseFileIsGoneMarksItMissing(t *testing.T) {
	q := queueImageWhileBusy(t)
	digest, _, _ := strings.Cut(strings.TrimPrefix(q.uploadID, "upload:"), "/")
	if err := os.RemoveAll(filepath.Join(uploadsDir(q.daemon.Registry), digest)); err != nil {
		t.Fatal(err)
	}
	response := postAction(t, q.handler, map[string]any{"action": "queue-cancel", "queueId": q.queueID})
	var answer struct {
		Result struct {
			Message     string             `json:"message"`
			Attachments []queuedUploadView `json:"attachments"`
		} `json:"result"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil || answer.Result.Message != "look at this" {
		t.Fatalf("cancel status=%d body=%s", response.Code, response.Body.String())
	}
	if len(answer.Result.Attachments) != 1 || answer.Result.Attachments[0].Name != "shot.png" || !answer.Result.Attachments[0].Missing {
		t.Fatalf("a swept attachment came back as %s", response.Body.String())
	}
}
