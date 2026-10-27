package surfaces

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) MoveToRoot(ctx context.Context, out *surface.MoveOut, moveID, root string) (*surface.Session, error) {
	conversation := out.Conversation
	if !claudeConversationID(conversation) || !claudeConversationID(moveID) {
		return nil, fmt.Errorf("Claude session %q or move %q is not a valid id", conversation, moveID)
	}
	target, err := surface.ResolveClaudeMoveRoot(c.home, root)
	if err != nil {
		return nil, err
	}
	source, err := c.conversationRoot(conversation)
	if err != nil {
		return nil, err
	}
	if source == "" {
		return nil, fmt.Errorf("Claude conversation %s has no transcript in any config root", conversation)
	}
	if source == target {
		return nil, fmt.Errorf("Claude conversation %s already runs under %s", conversation, root)
	}
	undo, err := surface.CarryClaudeProject(c.home, source, target, out.Cwd)
	if err != nil {
		return nil, fmt.Errorf("carry the project's Claude config to %s: %w", root, err)
	}
	if err := c.ArchiveMove(conversation, moveID); err != nil {
		return nil, errors.Join(fmt.Errorf("archive the conversation: %w", err), c.RestoreMove(conversation, moveID), undo())
	}
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return nil, c.unplaceRootMove(conversation, moveID, target, err, undo)
	}
	origin, _, err := c.moveOrigin(roots, conversation, moveID)
	if err == nil {
		err = c.placeRootMove(conversation, moveID, source, target, origin.ProjectDir)
	}
	if err != nil {
		return nil, c.unplaceRootMove(conversation, moveID, target, err, undo)
	}
	c.transcripts.pass(roots)(conversation)
	resumed, err := c.resumeAs(ctx, conversation, out.Cwd, claudehost.OpenRequest{Model: out.Model, PermissionMode: out.PermissionMode, Name: out.Title})
	if err != nil && surface.IsDeliveryOutcomeUnknown(err) {
		return nil, fmt.Errorf("%w: %v", surface.ErrMoveInPlaced, err)
	}
	if err != nil {
		return nil, c.unplaceRootMove(conversation, moveID, target, fmt.Errorf("%w: %v", surface.ErrMoveInRefused, err), undo)
	}
	_ = os.RemoveAll(c.movedDir(source, conversation, moveID))
	return resumed, nil
}

// The transcript goes last, so a crash leaves it in the archive, or in the target with every other part.
func (c *Claude) placeRootMove(conversation, moveID, source, target, projectDir string) error {
	parts := claudeMoveParts(conversation)
	order := append(append([]string{}, parts[1:]...), parts[0])
	for _, part := range order {
		if _, err := os.Lstat(claudeRootPart(target, projectDir, part)); err == nil {
			return fmt.Errorf("Claude conversation %s already has %s in %s", conversation, part, target)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	dir := c.movedDir(source, conversation, moveID)
	for _, part := range order {
		to := claudeRootPart(target, projectDir, part)
		if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
			return err
		}
		if err := renameIfPresent(filepath.Join(dir, filepath.FromSlash(part)), to); err != nil {
			return err
		}
	}
	return nil
}

func (c *Claude) unplaceRootMove(conversation, moveID, target string, cause error, undo func() error) error {
	if err := c.returnRootMove(conversation, moveID, target); err != nil {
		return errors.Join(cause, fmt.Errorf("%w: %v", surface.ErrRootMoveUnsettled, err))
	}
	return errors.Join(cause, undo())
}

func (c *Claude) returnRootMove(conversation, moveID, target string) error {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return err
	}
	origin, archived, err := c.moveOrigin(roots, conversation, moveID)
	if err != nil || !archived {
		return err
	}
	dir := c.movedDir(origin.Root, conversation, moveID)
	for _, part := range claudeMoveParts(conversation) {
		kept := filepath.Join(dir, filepath.FromSlash(part))
		if _, err := os.Lstat(kept); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(kept), 0700); err != nil {
			return err
		}
		if err := renameIfPresent(claudeRootPart(target, origin.ProjectDir, part), kept); err != nil {
			return err
		}
	}
	return c.RestoreMove(conversation, moveID)
}

func (c *Claude) RecoverRootMove(ctx context.Context, conversation, moveID, root string) (bool, error) {
	if !claudeConversationID(conversation) || !claudeConversationID(moveID) {
		return false, fmt.Errorf("Claude session %q or move %q is not a valid id", conversation, moveID)
	}
	unlock, err := c.lockConversation(ctx, conversation)
	if err != nil {
		return false, err
	}
	defer unlock()
	target, err := surface.ResolveClaudeMoveRoot(c.home, root)
	if err != nil {
		return false, c.RestoreMove(conversation, moveID)
	}
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return false, err
	}
	origin, archived, err := c.moveOrigin(roots, conversation, moveID)
	if err != nil {
		return false, err
	}
	if !archived {
		holding, err := c.conversationRoot(conversation)
		return holding == target, err
	}
	transcript := claudeMoveParts(conversation)[0]
	if _, err := os.Lstat(claudeRootPart(target, origin.ProjectDir, transcript)); err == nil {
		dir := c.movedDir(origin.Root, conversation, moveID)
		for _, part := range claudeMoveParts(conversation)[1:] {
			to := claudeRootPart(target, origin.ProjectDir, part)
			if _, err := os.Lstat(to); err == nil {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
				return false, err
			}
			if err := renameIfPresent(filepath.Join(dir, filepath.FromSlash(part)), to); err != nil {
				return false, err
			}
		}
		c.transcripts.pass(roots)(conversation)
		return true, os.RemoveAll(c.movedDir(origin.Root, conversation, moveID))
	}
	return false, c.returnRootMove(conversation, moveID, target)
}
