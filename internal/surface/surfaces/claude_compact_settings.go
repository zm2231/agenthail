package surfaces

import (
	"context"
	"fmt"
	"strconv"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) CompactSettings(ctx context.Context, sess *surface.Session) ([]surface.TurnSetting, error) {
	root, err := c.compactRoot(sess)
	if err != nil {
		return nil, err
	}
	var settings []surface.TurnSetting
	listed, hosted := c.hostedListing(ctx, sess)
	if hosted {
		enabled := true
		if listed.AutoCompact != nil {
			enabled = listed.AutoCompact.Enabled
		} else if listed.AutoCompactEnabled != nil {
			enabled = *listed.AutoCompactEnabled
		}
		settings = append(settings, surface.AutoCompactSetting(enabled))
	}
	file, err := surface.ReadClaudeCompactSettings(c.home, root.Root)
	if err != nil {
		return nil, err
	}
	_, window := claudeWindowFor(file, claudeSessionModels(sess, listed)...)
	settings = append(settings, surface.CompactWindowSetting(window, surface.TurnSettingAppliesNewSessions, root.Name))
	if claudeHosted(sess) {
		current, pending := listed.AutoCompactWindow, listed.PendingAutoCompactWindow
		if !hosted {
			kept, _ := c.hostSettings(sess.ID)
			current, pending = kept.AutoCompactWindow, kept.PendingAutoCompactWindow
		}
		if current == "" {
			current = surface.CompactWindowRootDefault
		}
		settings = append(settings, surface.SessionCompactWindowSetting(current, pending))
	}
	return settings, nil
}

func (c *Claude) SetCompactSetting(ctx context.Context, sess *surface.Session, key, value string) error {
	switch key {
	case surface.CompactSettingAuto:
		if value != surface.CompactAutoOn && value != surface.CompactAutoOff {
			return fmt.Errorf("%w: autoCompact is on or off; got %q", surface.ErrInvalidTurnSetting, value)
		}
		if !claudeHosted(sess) {
			return fmt.Errorf("%w: only a session the Claude host runs turns autocompact on or off here", surface.ErrUnsupported)
		}
		if err := c.EnsureWritable(ctx, sess); err != nil {
			return err
		}
		_, err := c.host.client.SetAutoCompact(ctx, sess.ID, value == surface.CompactAutoOn)
		c.host.forget()
		if err != nil {
			return claudeHostDeliveryError(err)
		}
		return nil
	case surface.CompactSettingSessionWindow:
		if value != surface.CompactWindowRootDefault {
			if err := surface.ValidCompactWindow(value); err != nil {
				return err
			}
		}
		if !claudeHosted(sess) {
			return fmt.Errorf("%w: only a session the Claude host runs takes its own compact window", surface.ErrUnsupported)
		}
		if err := c.EnsureWritable(ctx, sess); err != nil {
			return err
		}
		_, err := c.host.client.SetAutoCompactWindow(ctx, sess.ID, value)
		c.host.forget()
		if err != nil {
			return claudeHostDeliveryError(err)
		}
		return nil
	case surface.CompactSettingWindow:
		if err := surface.ValidCompactWindow(value); err != nil {
			return err
		}
		root, err := c.compactRoot(sess)
		if err != nil {
			return err
		}
		file, err := surface.ReadClaudeCompactSettings(c.home, root.Root)
		if err != nil {
			return err
		}
		listed, _ := c.hostedListing(ctx, sess)
		key, _ := claudeWindowFor(file, claudeSessionModels(sess, listed)...)
		_, err = surface.WriteClaudeCompactSettings(c.home, root.Root, surface.ClaudeCompactChange{Model: key, Window: value})
		return err
	}
	return fmt.Errorf("%w: unknown compact setting %q", surface.ErrInvalidTurnSetting, key)
}

func (c *Claude) hostedListing(ctx context.Context, sess *surface.Session) (claudehost.Session, bool) {
	if !claudeHosted(sess) {
		return claudehost.Session{}, false
	}
	return c.host.session(ctx, sess.ID, sess.PID)
}

// Claude names the model it ran only after a turn; before that the chosen model stands in.
func claudeSessionModels(sess *surface.Session, listed claudehost.Session) []string {
	return []string{listed.ContextModel, listed.Model, sess.ConfiguredModel}
}

func (c *Claude) compactRoot(sess *surface.Session) (surface.ClaudeConfigRoot, error) {
	path, err := c.conversationRoot(sess.ID)
	if err != nil {
		return surface.ClaudeConfigRoot{}, err
	}
	roots, err := surface.DescribeClaudeConfigRoots(c.home)
	if err != nil {
		return surface.ClaudeConfigRoot{}, err
	}
	if path == "" {
		return roots[0], nil
	}
	if root, ok := surface.ClaudeConfigRootFor(roots, path); ok {
		return root, nil
	}
	return roots[0], nil
}

func claudeWindowFor(file surface.ClaudeCompactSettings, models ...string) (string, string) {
	for _, model := range models {
		if window, ok := file.ModelWindows[model]; ok && model != "" {
			return model, claudeWindowValue(window)
		}
	}
	if file.AutoCompactWindow != nil {
		return "", claudeWindowValue(*file.AutoCompactWindow)
	}
	return "", ""
}

func claudeWindowValue(window surface.ClaudeCompactWindow) string {
	switch {
	case window.Auto:
		return surface.CompactAuto
	case window.Tokens > 0:
		return strconv.FormatInt(window.Tokens, 10)
	}
	return ""
}
