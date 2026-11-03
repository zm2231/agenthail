package daemon

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func BenchmarkACredentialPassWithNothingToChangeAt200Credentials(b *testing.B) {
	r, err := registry.Open(registrytest.Path(b))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { r.Close() })
	d := newTestDaemon(b, r, nil)
	d.attachMCPCredentials("127.0.0.1:1")
	for i := range 200 {
		id := fmt.Sprintf("bench-%03d", i)
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Cwd: "/work"}); err != nil {
			b.Fatal(err)
		}
		if _, err := r.IssueSessionCredential(id, sessionCapabilities(false), json.RawMessage(`{}`), time.Now()); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for b.Loop() {
		rotated, err := d.reconcileCredentials()
		if err != nil || len(rotated) != 0 {
			b.Fatalf("rotated=%d err=%v", len(rotated), err)
		}
	}
}
