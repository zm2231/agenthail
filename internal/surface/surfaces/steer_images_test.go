package surfaces

import (
	"context"
	"errors"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestOnlyAHostedClaudeSessionTakesImagesInASteer(t *testing.T) {
	c := NewClaude(nil, t.TempDir())
	hosted := &surface.Session{ID: "h", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost}
	bridged := &surface.Session{ID: "b", Surface: surface.KindClaude, Bridge: "bridge"}
	if !c.SessionCapabilities(hosted).SteerImages {
		t.Fatal("a hosted session should take images in a steer")
	}
	if c.SessionCapabilities(bridged).SteerImages {
		t.Fatal("a Remote Control session holds images until a mid-turn post is checked")
	}
	image := surface.Uploads{{Name: "shot.png", MediaType: "image/png", Path: "/tmp/shot.png"}}
	if _, err := c.Control(context.Background(), bridged, surface.ControlRequest{Action: "steer", Value: "look", Images: image}); !errors.Is(err, surface.ErrUnsupported) {
		t.Fatalf("bridge steer with an image err=%v", err)
	}
}
