package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type attachmentAPISurface struct {
	*daemonSurface
	data []byte
}

func (s *attachmentAPISurface) ReadAttachment(context.Context, *surface.Session, string) (*surface.Attachment, []byte, error) {
	return &surface.Attachment{ID: "attachment:test", MediaType: "image/png", Width: 1, Height: 1, Bytes: int64(len(s.data))}, s.data, nil
}

func TestSessionAttachmentAPIUsesReadAuthAndReturnsPrivateBytes(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	from.Surface = surface.KindClaude
	if err := registry.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	fake.kind = surface.KindClaude
	adapter := &attachmentAPISurface{daemonSurface: fake, data: []byte("png")}
	d := New(registry, []surface.Surface{adapter})
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/session-attachment?sessionId="+from.ID+"&id=attachment:test", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/png" || response.Header.Get("Cache-Control") != "private, immutable, max-age=31536000" {
		t.Fatalf("status=%d headers=%v", response.StatusCode, response.Header)
	}
	unauthorized, err := http.Get(server.URL + "/api/v1/session-attachment?sessionId=" + from.ID + "&id=attachment:test")
	if err != nil {
		t.Fatal(err)
	}
	defer unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.StatusCode)
	}
}
