package daemon

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
)

type failingBody struct {
	io.Reader
}

func (b failingBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		return n, errors.New("connection reset")
	}
	return n, err
}

type untouchedBody struct{ read *bool }

func (b untouchedBody) Read([]byte) (int, error) {
	*b.read = true
	select {}
}

func TestAnOverLongIdempotencyKeyIsRefusedBeforeTheBodyIsRead(t *testing.T) {
	for _, front := range parityFormats {
		t.Run(front.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			d, _, _, _, _ := daemonFixture(t)
			read := false
			request := httptest.NewRequest(http.MethodPost, front.path, untouchedBody{&read})
			request.Header.Set(actionIdempotencyHeader, strings.Repeat("k", registry.ActionReceiptKeyLimit+1))
			recorder := httptest.NewRecorder()
			d.actionsHTTPHandler(front.format)(recorder, request)
			if read || recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_idempotency_key") {
				t.Fatalf("read=%v status=%d body=%s", read, recorder.Code, recorder.Body)
			}
		})
	}
}

func TestAnActionWhoseBodyFailsToArriveIsRefusedWithoutRunning(t *testing.T) {
	send := `{"action":"send","sessionId":"to","message":"hello"}`
	for _, front := range parityFormats {
		for _, key := range []string{"", "body-fails"} {
			t.Run(front.name+" key="+key, func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				d, _, fake, _, _ := daemonFixture(t)
				request := httptest.NewRequest(http.MethodPost, front.path, failingBody{strings.NewReader(send)})
				if key != "" {
					request.Header.Set(actionIdempotencyHeader, key)
				}
				recorder := httptest.NewRecorder()
				d.actionsHTTPHandler(front.format)(recorder, request)
				want := "invalid dashboard request"
				if key != "" {
					want = "The action request is invalid."
				}
				if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), want) {
					t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
				}
				if len(fake.sent) != 0 {
					t.Fatalf("a request that failed to arrive was sent: %v", fake.sent)
				}
				if key == "" {
					return
				}
				retry := httptest.NewRequest(http.MethodPost, front.path, strings.NewReader(send))
				retry.Header.Set(actionIdempotencyHeader, key)
				recorder = httptest.NewRecorder()
				d.actionsHTTPHandler(front.format)(recorder, retry)
				if recorder.Code != http.StatusOK || len(fake.sent) != 1 {
					t.Fatalf("the key was held by the failed request: status=%d body=%s sent=%v", recorder.Code, recorder.Body, fake.sent)
				}
			})
		}
	}
}
