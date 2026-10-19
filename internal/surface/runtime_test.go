package surface

import (
	"strings"
	"testing"
)

type kindFake struct {
	Surface
	name  SurfaceKind
	kinds []RuntimeKind
}

func (f kindFake) Name() SurfaceKind           { return f.name }
func (f kindFake) RuntimeKinds() []RuntimeKind { return f.kinds }

func TestAdaptersForResolvesByKindAndNamesTheSessionItCannotServe(t *testing.T) {
	desktop := kindFake{name: KindCodex, kinds: []RuntimeKind{RuntimeCodexDesktop}}
	appServer := kindFake{name: KindCodex, kinds: []RuntimeKind{RuntimeCodexAppServer}}
	adapters := Adapters{nil, desktop, appServer}
	if got, err := adapters.For(&Session{ID: "thread", Surface: KindCodex, RuntimeKind: RuntimeCodexAppServer}); err != nil || got.(kindFake).kinds[0] != RuntimeCodexAppServer {
		t.Fatalf("adapter=%v err=%v, want the app-server adapter", got, err)
	}
	for _, test := range []struct {
		session Session
		want    string
	}{
		{Session{ID: "kindless", Surface: KindCodex}, "session kindless has no runtime kind"},
		{Session{ID: "unserved", Surface: KindClaude, RuntimeKind: RuntimeClaudeCLI}, `no adapter serves runtime "claude-cli" of session unserved`},
	} {
		if adapter, err := adapters.For(&test.session); adapter != nil || err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("session=%s adapter=%v err=%v, want %q", test.session.ID, adapter, err, test.want)
		}
	}
	if adapter, err := adapters.For(nil); adapter != nil || err == nil {
		t.Fatalf("nil session adapter=%v err=%v", adapter, err)
	}
	if got := adapters.ForSurface(KindCodex); got == nil || got.Name() != KindCodex || adapters.ForSurface(KindNotion) != nil {
		t.Fatalf("ForSurface codex=%v notion=%v", got, adapters.ForSurface(KindNotion))
	}
}
