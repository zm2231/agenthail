package daemon

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func droppingDashboardProxy(t *testing.T, router http.Handler) (url string, drop *atomic.Bool, kept *atomic.Pointer[string]) {
	t.Helper()
	drop, kept = &atomic.Bool{}, &atomic.Pointer[string]{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "example.test"
		r.Header.Set("Origin", "http://example.test")
		addBrowserCookie(router, r)
		answer := httptest.NewRecorder()
		router.ServeHTTP(answer, r)
		if drop.Swap(false) {
			body := answer.Body.String()
			kept.Store(&body)
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			connection.Close()
			return
		}
		w.WriteHeader(answer.Code)
		_, _ = w.Write(answer.Body.Bytes())
	}))
	t.Cleanup(server.Close)
	return server.URL, drop, kept
}

func postWithKey(url, key, body string) (int, string, error) {
	request, err := http.NewRequest(http.MethodPost, url+"/api/action", strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	client := http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(response.Body)
	return response.StatusCode, string(answer), err
}

func TestALostAnswerRetriedUnderItsKeyReturnsTheOriginalResultAndDeliversOnce(t *testing.T) {
	f := newHumanTurnFixture(t)
	url, drop, kept := droppingDashboardProxy(t, dashboardRouter(f.d))
	const body = `{"action":"send","author":"person","sessionId":"conversation-bridge","message":"Please inspect the failure."}`

	drop.Store(true)
	if status, answer, err := postWithKey(url, "lost-answer", body); err == nil {
		t.Fatalf("the first answer was not lost: status=%d body=%s", status, answer)
	}
	if len(f.posts) != 1 {
		t.Fatalf("the action was not applied before its answer was lost: posts=%q", f.posts)
	}
	assertHumanTurnPost(t, f.posts[0], "Please inspect the failure.")

	status, answer, err := postWithKey(url, "lost-answer", body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("retry: status=%d body=%s err=%v", status, answer, err)
	}
	if original := kept.Load(); original == nil || answer != *original {
		t.Fatalf("retry body=%s, want the original %v", answer, kept.Load())
	}
	if len(f.posts) != 1 {
		t.Fatalf("the retry delivered again: posts=%q", f.posts)
	}

	if status, answer, err := postWithKey(url, "a-new-message", body); err != nil || status != http.StatusOK || len(f.posts) != 2 {
		t.Fatalf("a new key must deliver: status=%d body=%s err=%v posts=%q", status, answer, err, f.posts)
	}
}

func TestALostRefusalRetriedUnderItsKeyReplaysTheRefusal(t *testing.T) {
	f := newHumanTurnFixture(t)
	url, drop, kept := droppingDashboardProxy(t, dashboardRouter(f.d))
	const body = `{"action":"send","author":"person","sessionId":"no-such-session","message":"hello"}`

	drop.Store(true)
	if status, answer, err := postWithKey(url, "lost-refusal", body); err == nil {
		t.Fatalf("the first answer was not lost: status=%d body=%s", status, answer)
	}
	status, answer, err := postWithKey(url, "lost-refusal", body)
	if err != nil || status < 400 {
		t.Fatalf("retry: status=%d body=%s err=%v", status, answer, err)
	}
	if original := kept.Load(); original == nil || answer != *original {
		t.Fatalf("retry body=%s, want the original %v", answer, kept.Load())
	}
	if len(f.posts) != 0 {
		t.Fatalf("posts=%q", f.posts)
	}
}
