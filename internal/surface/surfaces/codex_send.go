package surfaces

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) Send(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	return c.SendWithOptions(ctx, sess, message, surface.SendOptions{})
}

func (c *Codex) SendWithOptions(ctx context.Context, sess *surface.Session, message string, options surface.SendOptions) (*surface.SendResult, error) {
	if err := options.TurnOptions.Validate(surface.KindCodex); err != nil {
		return nil, surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	}
	if err := refuseCodexComposerCommand(message); err != nil {
		return nil, err
	}
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	conn, err := c.openWriter(ctx, sess)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	active, err := c.activeTurnID(ctx, conn, sess.ID)
	if err != nil {
		return nil, surface.DeliveryUnavailable(fmt.Errorf("inspect active turn: %w", err))
	}
	if active != "" {
		return &surface.SendResult{UUID: sess.ID, Accepted: false}, nil
	}
	params := map[string]any{
		"threadId": sess.ID,
		"input":    codexUserInput(message, options.Images),
	}
	model, notice := options.Model, ""
	_, pending := loadCodexPendingModel(sess.ID)
	if pending && model == "" {
		current, err := codexThreadModel(ctx, conn, sess.ID)
		if err != nil {
			return nil, surface.DeliveryUnavailable(fmt.Errorf("read the model the pending choice replaces: %w", err))
		}
		model, notice = codexPendingModelForTurn(sess.ID, current)
	}
	if err := applyCodexTurnOptions(ctx, conn, sess, model, options.TurnOptions, params); err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	resp, err := conn.Request(ctx, "turn/start", params, 10*time.Second)
	if err != nil {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("turn/start: %w", err))
	}
	if pending {
		if err := clearCodexPendingModel(sess.ID); err != nil {
			notice = strings.TrimSpace(notice + " The pending model choice could not be cleared: " + err.Error())
		}
	}
	turnID := sess.ID
	if result, _ := resp["result"].(map[string]any); result != nil {
		if turn, _ := result["turn"].(map[string]any); turn != nil && str(turn, "id") != "" {
			turnID = str(turn, "id")
		} else if value := str(result, "turnId"); value != "" {
			turnID = value
		}
	}
	return &surface.SendResult{UUID: turnID, Accepted: true, Notice: notice}, nil
}

// NativeImages is true on every route: each Codex transport speaks the
// app-server protocol, whose turn input takes local image paths.
func (c *Codex) NativeImages(*surface.Session, surface.Route) bool { return true }

// refuseCodexComposerCommand refuses a message that reads as a Codex slash
// command. Codex runs those only in its own composer; turn input passes the
// text to the model as a prompt, so the command never runs. As in that
// composer, text that starts with a space or whose first word holds another
// slash, such as a path, is a prompt.
func refuseCodexComposerCommand(message string) error {
	rest, ok := strings.CutPrefix(message, "/")
	if !ok {
		return nil
	}
	name := rest
	if end := strings.IndexFunc(rest, unicode.IsSpace); end >= 0 {
		name = rest[:end]
	}
	if name == "" || strings.Contains(name, "/") {
		return nil
	}
	return surface.DeliveryTerminal(fmt.Errorf("Codex runs /%s only in its own composer; sent here it would reach the model as plain text and nothing would run. To use a skill named %s, send $%s", name, name, name), surface.DeliveryInvalidRequest)
}

func codexUserInput(message string, images surface.Uploads) []map[string]any {
	input := make([]map[string]any, 0, 1+len(images))
	if message != "" || len(images) == 0 {
		input = append(input, map[string]any{"type": "text", "text": message})
	}
	for _, image := range images {
		input = append(input, map[string]any{"type": "localImage", "path": image.Path})
	}
	return input
}

func codexDirectInputAccepted(response map[string]any) bool {
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	accepts, _ := thread["canAcceptDirectInput"].(bool)
	return accepts
}

func isCodexTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "app-server request timed out")
}
