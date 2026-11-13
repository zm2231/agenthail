package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestASentFileDownloadsSandboxedAndASentPDFOpensInline(t *testing.T) {
	d, registry, _, from, _ := daemonFixture(t)
	dir := t.TempDir()
	pdf := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF\n")
	page := []byte("<html><script>alert(1)</script></html>")
	for name, data := range map[string][]byte{"report.pdf": pdf, "page.html": page} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	record, _ := json.Marshal(map[string]any{
		"type": "user", "uuid": "u1",
		"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call-send", "content": "delivered"}}},
		"toolUseResult": map[string]any{"attachments": []any{
			map[string]any{"path": filepath.Join(dir, "report.pdf"), "size": len(pdf), "isImage": false, "media_type": "application/pdf"},
			map[string]any{"path": filepath.Join(dir, "page.html"), "size": len(page), "isImage": false, "media_type": "text/html"},
		}},
	})
	call, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": "a1", "message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool_use", "id": "call-send", "name": "SendUserFile", "input": map[string]any{"files": []string{"report.pdf", "page.html"}, "status": "normal"}},
	}}})
	transcript := filepath.Join(t.TempDir(), "claude.jsonl")
	if err := os.WriteFile(transcript, append(append(append(call, '\n'), record...), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	from.Surface, from.Transcript, from.RuntimeKind = surface.KindClaude, transcript, surface.RuntimeClaudeCLI
	if err := registry.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	claude := surfaces.NewClaude(nil, t.TempDir())
	d.Surfaces = []surface.Surface{claude}
	read, err := claude.ReadSession(context.Background(), &from, surface.SessionReadRequest{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, item := range read.Items {
		if item.Kind == "file" && item.Attachment != nil {
			ids[item.Attachment.Name] = item.Attachment.ID
		}
	}
	handler := dashboardRouter(d)
	serve := func(id string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session-attachment?sessionId=from&id="+id, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	response := serve(ids["report.pdf"])
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/pdf" || response.Header().Get("Content-Disposition") != `inline; filename=report.pdf` ||
		slices.Contains(response.Header().Values("Content-Security-Policy"), "sandbox") || response.Header().Get("X-Content-Type-Options") != "nosniff" || !bytes.Equal(response.Body.Bytes(), pdf) {
		t.Fatalf("pdf: status=%d headers=%v", response.Code, response.Header())
	}
	response = serve(ids["page.html"])
	if response.Code != http.StatusOK || response.Header().Get("Content-Disposition") != `attachment; filename=page.html` ||
		!slices.Contains(response.Header().Values("Content-Security-Policy"), "sandbox") || len(response.Header().Values("Content-Security-Policy")) != 2 || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("html: status=%d headers=%v", response.Code, response.Header())
	}
}

type attachingSurface struct {
	*federatedSurface
	data atomic.Pointer[[]byte]
}

func (s *attachingSurface) ReadAttachment(context.Context, *surface.Session, string) (*surface.Attachment, []byte, error) {
	data := *s.data.Load()
	return &surface.Attachment{ID: "sent", MediaType: "text/html", Bytes: int64(len(data)), Name: "page.html"}, data, nil
}

func TestAPairedHostsSentFileArrivesWholeAndSandboxedOrNotAtAll(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	local, remote := newFederationNode(t, "local-a"), newFederationNode(t, "remote-a")
	page := append([]byte("<html><script>alert(1)</script>"), bytes.Repeat([]byte("x"), 33<<20)...)
	attaching := &attachingSurface{federatedSurface: remote.fake}
	attaching.data.Store(&page)
	remote.daemon.Surfaces = []surface.Surface{attaching}
	remote.serve(t, nil)
	local.start(t, testRoots(remote.server))
	record := local.pair(t, remote, "vps")
	testkit.Eventually(t, "remote session mirrored", 10*time.Second, func() bool { return len(sessionsOf(local.snapshot(t), record.ID)) == 1 })
	qualified := url.QueryEscape(hosts.Qualify(record.ID, "remote-a"))
	for _, route := range []string{"/api/v1/session-attachment", "/api/session-attachment"} {
		response := local.request(t, http.MethodGet, route+"?sessionId="+qualified+"&id=sent", nil)
		policies := response.Header().Values("Content-Security-Policy")
		if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), page) || !slices.Contains(policies, "sandbox") || !slices.ContainsFunc(policies, func(policy string) bool { return strings.Contains(policy, "frame-ancestors 'none'") }) ||
			response.Header().Get("Content-Disposition") != `attachment; filename=page.html` || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: status=%d bytes=%d headers=%v", route, response.Code, response.Body.Len(), response.Header())
		}
	}
	over := bytes.Repeat([]byte("x"), int(surfaces.MaxSentFileBytes)+1)
	attaching.data.Store(&over)
	if response := local.request(t, http.MethodGet, "/api/v1/session-attachment?sessionId="+qualified+"&id=sent", nil); response.Code != http.StatusBadGateway {
		t.Fatalf("an answer over the cap: status=%d bytes=%d", response.Code, response.Body.Len())
	}
}
