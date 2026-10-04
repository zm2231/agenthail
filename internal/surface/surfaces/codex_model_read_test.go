package surfaces

import (
	"context"
	"slices"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexModelReadUsesMetadataWithoutResuming(t *testing.T) {
	for _, test := range []struct {
		name, model string
	}{
		{name: "metadata names the model", model: "metadata-model"},
		{name: "metadata omits the model"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bridge := startDesktopBridge(t, func(method string) string {
				switch method {
				case "thread/read":
					return `{"result":{"thread":{"id":"thread","model":"` + test.model + `"}}}`
				case "thread/resume":
					return `{"result":{"model":"resumed-model"}}`
				}
				return ""
			})
			model, err := NewCodex(bridge.URL).Model(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop}, "")
			if test.model == "" {
				if err == nil {
					t.Fatalf("missing model metadata was reported as %q", model)
				}
			} else if err != nil || model != test.model {
				t.Fatalf("model=%q err=%v", model, err)
			}
			if slices.Contains(bridge.Methods(), "thread/resume") {
				t.Fatalf("reading the model resumed the thread: %v", bridge.Methods())
			}
		})
	}
}
