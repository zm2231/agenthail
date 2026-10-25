package surfaces

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

// EnsureWritable refuses a conversation that more than one config root
// holds, since a send or control could reach either copy. Every write path of
// the adapter checks it itself as well, whoever calls it.
func (c *Claude) EnsureWritable(_ context.Context, sess *surface.Session) error {
	if sess == nil {
		return nil
	}
	if _, err := c.conversationRoot(sess.ID); err != nil {
		return err
	}
	return c.ensureOneHolder(sess)
}

func (c *Claude) Send(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	if err := peerSlashCommand(sess, message); err != nil {
		return nil, err
	}
	if err := c.EnsureWritable(ctx, sess); err != nil {
		return nil, err
	}
	if _, err := c.reach(ctx, sess); err != nil {
		return nil, err
	}
	if sess.Transport == "uds" {
		if err := peerSlashCommand(sess, message); err != nil {
			return nil, err
		}
		return c.sendPeer(ctx, sess, message)
	}
	return c.postWhenIdle(ctx, sess, message, nil)
}

// peerSlashCommand rejects a slash command for the peer socket, which cannot
// run one. It is checked before reaching the session and again on the
// transport the session was reached through.
func peerSlashCommand(sess *surface.Session, message string) error {
	if sess.Transport == "uds" && strings.HasPrefix(strings.TrimSpace(message), "/") {
		return surface.DeliveryTerminal(fmt.Errorf("Claude peer messages cannot execute slash commands; use the Claude session directly"), surface.DeliveryInvalidRequest)
	}
	return nil
}

func (c *Claude) postWhenIdle(ctx context.Context, sess *surface.Session, message string, images surface.Uploads) (*surface.SendResult, error) {
	message = surface.LabelCLI(ctx, message)
	observation, err := c.Observe(ctx, sess)
	if err != nil {
		return nil, err
	}
	if observation.Status != surface.StatusIdle || observation.ActiveTurnID != "" {
		return &surface.SendResult{UUID: sess.ID, Accepted: false}, nil
	}
	content, err := claudeUserContent(message, images)
	if err != nil {
		return nil, err
	}
	return c.postContent(ctx, sess, content)
}

// NativeImages is true only for a person's turn over the web bridge, which
// carries Messages API content blocks. The peer socket carries text.
func (c *Claude) NativeImages(_ *surface.Session, route surface.Route) bool {
	return route == surface.RouteHumanTurn
}

// claudeUserContent is the user message content: plain text, or image
// blocks followed by the text when the turn carries images.
func claudeUserContent(message string, images surface.Uploads) (any, error) {
	if len(images) == 0 {
		return message, nil
	}
	blocks := make([]map[string]any, 0, len(images)+1)
	for _, image := range images {
		data, err := os.ReadFile(image.Path)
		if err != nil {
			return nil, surface.DeliveryTerminal(fmt.Errorf("read attached image %s: %w", image.Name, err), surface.DeliveryInvalidRequest)
		}
		blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": image.MediaType, "data": base64.StdEncoding.EncodeToString(data)}})
	}
	if message != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": message})
	}
	return blocks, nil
}

// Route sends a person's message as their own turn through the session host or the
// session's web bridge identity, the routes that record it as the user's turn. A
// session with no live process resumes in the host, so a person's message to it
// is a turn too. Other messages to a session with a native peer socket take that
// socket.
func (c *Claude) Route(sess *surface.Session, author surface.Author) surface.Route {
	if author == surface.AuthorPerson && c.canSendHumanTurn(sess) {
		return surface.RouteHumanTurn
	}
	if sess != nil && sess.Transport == "uds" {
		return surface.RoutePeer
	}
	return surface.RouteSend
}

func (c *Claude) canSendHumanTurn(sess *surface.Session) bool {
	return sess != nil && sess.Surface == surface.KindClaude && (claudeHosted(sess) || !claudeProcessAlive(sess.PID) || !c.localOnly && !nativeClaudeOnly(sess))
}

// SendHumanTurn submits a message through Claude's authenticated web bridge as
// a user event, even when the session also has a native peer socket.
func (c *Claude) SendHumanTurn(ctx context.Context, sess *surface.Session, message string, images surface.Uploads) (*surface.SendResult, error) {
	if sess == nil {
		return nil, surface.DeliveryTerminal(errors.New("Claude session is required"), surface.DeliveryInvalidRequest)
	}
	if err := c.EnsureWritable(ctx, sess); err != nil {
		return nil, err
	}
	if !c.canSendHumanTurn(sess) {
		return nil, surface.DeliveryUnavailable(errors.New("Claude web bridge session is unavailable"))
	}
	if _, err := c.reach(ctx, sess); err != nil {
		return nil, err
	}
	if claudeHosted(sess) {
		return c.sendHostTurn(ctx, sess, message, "", images)
	}
	if !c.canSendHumanTurn(sess) {
		return nil, surface.DeliveryUnavailable(errors.New("Claude resumed without its web bridge; the message is routed again on retry"))
	}
	return c.postWhenIdle(ctx, sess, message, images)
}
