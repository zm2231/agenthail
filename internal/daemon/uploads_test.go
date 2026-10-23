package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/uploads"
)

// imageSurface takes images as model input on every route, like Codex.
type imageSurface struct {
	*daemonSurface
	images []surface.Uploads
}

func (*imageSurface) NativeImages(*surface.Session, surface.Route) bool { return true }

func (f *imageSurface) SendWithOptions(ctx context.Context, session *surface.Session, message string, options surface.SendOptions) (*surface.SendResult, error) {
	result, err := f.daemonSurface.SendWithOptions(ctx, session, message, options)
	if err == nil && result != nil && result.Accepted {
		f.images = append(f.images, options.Images)
	}
	return result, err
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := range width {
		canvas.Set(x, 0, color.RGBA{R: 200, A: 255})
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

type uploadAnswer struct {
	Upload struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		MediaType string `json:"mediaType"`
		Bytes     int64  `json:"bytes"`
		Width     int    `json:"width"`
	} `json:"upload"`
}

func postUpload(t *testing.T, handler http.Handler, sessionID, name, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/uploads?sessionId="+url.QueryEscape(sessionID)+"&name="+url.QueryEscape(name), bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeUpload(t *testing.T, response *httptest.ResponseRecorder) uploadAnswer {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", response.Code, response.Body.String())
	}
	var answer uploadAnswer
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	return answer
}

func postAction(t *testing.T, handler http.Handler, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(encoded))
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func uploadFixture(t *testing.T) (*Daemon, *imageSurface, http.Handler) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, _, base, _, _ := daemonFixture(t)
	base.caps = surface.Capabilities{Send: true, Steer: true, Attach: true}
	adapter := &imageSurface{daemonSurface: base}
	d.Surfaces = []surface.Surface{adapter}
	return d, adapter, d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
}

func TestUploadedImageReachesRuntimeAsImageAndFileAsStoredPath(t *testing.T) {
	d, adapter, handler := uploadFixture(t)
	picture := decodeUpload(t, postUpload(t, handler, "to", "IMG 0001.HEIC", "image/png", testPNG(t, 64, 32)))
	if picture.Upload.MediaType != "image/png" || picture.Upload.Width != 64 || picture.Upload.Name != "IMG-0001.png" {
		t.Fatalf("image upload=%+v", picture.Upload)
	}
	again := decodeUpload(t, postUpload(t, handler, "to", "IMG 0001.HEIC", "image/png", testPNG(t, 64, 32)))
	if again.Upload.ID != picture.Upload.ID {
		t.Fatalf("retried upload id=%q, first=%q", again.Upload.ID, picture.Upload.ID)
	}
	notes := decodeUpload(t, postUpload(t, handler, "to", "../notes.txt", "text/plain", []byte("synthetic notes\n")))
	if notes.Upload.Name != "notes.txt" || strings.Contains(notes.Upload.MediaType, "image") {
		t.Fatalf("file upload=%+v", notes.Upload)
	}
	stored := uploadsDir(d.Registry)
	for _, response := range []string{picture.Upload.ID, notes.Upload.ID} {
		if strings.Contains(response, stored) {
			t.Fatalf("upload id exposes the stored path: %s", response)
		}
	}

	send := postAction(t, handler, map[string]any{"action": "send", "sessionId": "to", "message": "compare these", "author": "person", "attachments": []string{picture.Upload.ID, notes.Upload.ID}})
	if send.Code != http.StatusOK {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body.String())
	}
	if len(adapter.images) != 1 || len(adapter.images[0]) != 1 || adapter.images[0][0].Path != filepath.Join(stored, strings.TrimPrefix(picture.Upload.ID, "upload:")) {
		t.Fatalf("runtime images=%+v", adapter.images)
	}
	want := "compare these\n\nAttached file: " + filepath.Join(stored, strings.TrimPrefix(notes.Upload.ID, "upload:"))
	if got := adapter.sent[len(adapter.sent)-1]; got != want {
		t.Fatalf("runtime message=%q want %q", got, want)
	}

	imageOnly := postAction(t, handler, map[string]any{"action": "send", "sessionId": "to", "attachments": []string{picture.Upload.ID}})
	if imageOnly.Code != http.StatusOK || adapter.sent[len(adapter.sent)-1] != "" || len(adapter.images) != 2 {
		t.Fatalf("image-only send status=%d body=%s sent=%q", imageOnly.Code, imageOnly.Body.String(), adapter.sent)
	}
}

func TestUploadAndSendRefuseWhatTheRuntimeCannotTake(t *testing.T) {
	_, adapter, handler := uploadFixture(t)
	if response := postUpload(t, handler, "to", "empty.txt", "text/plain", nil); response.Code != http.StatusBadRequest {
		t.Fatalf("empty upload status=%d body=%s", response.Code, response.Body.String())
	}
	large := append(testPNG(t, 8, 8), bytes.Repeat([]byte{0}, int(uploads.MaxImageBytes))...)
	if response := postUpload(t, handler, "to", "large.png", "image/png", large); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized image status=%d body=%s", response.Code, response.Body.String())
	}
	missing := postAction(t, handler, map[string]any{"action": "send", "sessionId": "to", "message": "here", "attachments": []string{"upload:" + strings.Repeat("a", 64) + "/gone.png"}})
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "upload it again") {
		t.Fatalf("unknown attachment status=%d body=%s", missing.Code, missing.Body.String())
	}
	steer := postAction(t, handler, map[string]any{"action": "steer", "sessionId": "to", "message": "now", "attachments": []string{"upload:x"}})
	if steer.Code != http.StatusBadRequest {
		t.Fatalf("steer with attachments status=%d body=%s", steer.Code, steer.Body.String())
	}
	adapter.caps.Attach = false
	if response := postUpload(t, handler, "to", "a.txt", "text/plain", []byte("x")); response.Code != http.StatusConflict {
		t.Fatalf("upload to a runtime without attachments status=%d body=%s", response.Code, response.Body.String())
	}
	if len(adapter.sent) != 0 {
		t.Fatalf("refused requests reached the runtime: %q", adapter.sent)
	}
}

func TestQueuedSendKeepsItsAttachments(t *testing.T) {
	d, adapter, handler := uploadFixture(t)
	picture := decodeUpload(t, postUpload(t, handler, "to", "shot.png", "image/png", testPNG(t, 4, 4)))
	adapter.accepted = false
	queued := postAction(t, handler, map[string]any{"action": "send", "sessionId": "to", "message": "when free", "busyDelivery": "steer", "attachments": []string{picture.Upload.ID}})
	if queued.Code != http.StatusOK || !strings.Contains(queued.Body.String(), `"queued"`) {
		t.Fatalf("busy send status=%d body=%s", queued.Code, queued.Body.String())
	}
	if len(adapter.steered) != 0 {
		t.Fatalf("a send with attachments was steered into the running turn: %q", adapter.steered)
	}
	adapter.accepted = true
	session, err := d.Registry.Session("to")
	if err != nil {
		t.Fatal(err)
	}
	d.drainMessageQueue(context.Background(), adapter, session)
	if len(adapter.images) != 1 || len(adapter.images[0]) != 1 || adapter.images[0][0].MediaType != "image/png" || adapter.sent[len(adapter.sent)-1] != surface.CLILabel+"when free" {
		t.Fatalf("drained images=%+v sent=%q", adapter.images, adapter.sent)
	}
}

func TestFederationUploadLandsOnTheHostThatOwnsTheSession(t *testing.T) {
	local, remote, record := pairedFederation(t)
	remote.fake.caps.Attach = true
	local.fake.caps.Attach = true
	qualified := hosts.Qualify(record.ID, "remote-a")
	answer := decodeUpload(t, postUpload(t, local.handler, qualified, "report.txt", "text/plain", []byte("synthetic report\n")))
	remotePath := filepath.Join(uploadsDir(remote.reg), strings.TrimPrefix(answer.Upload.ID, "upload:"))
	if _, err := remote.daemon.uploads.Resolve(answer.Upload.ID); err != nil {
		t.Fatalf("upload is not stored on the owning host: %v", err)
	}
	if _, err := local.daemon.uploads.Resolve(answer.Upload.ID); err == nil {
		t.Fatal("upload for a remote session was stored on this host")
	}
	send := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": qualified, "message": "read this", "attachments": []string{answer.Upload.ID}})
	if send.Code != http.StatusOK {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body.String())
	}
	delivered := remote.fake.delivered()
	if len(delivered) != 1 || delivered[0] != surface.CLILabel+"read this\n\nAttached file: "+remotePath {
		t.Fatalf("remote delivered=%q", delivered)
	}
}
