package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const rootMoveRecoverTimeout = 30 * time.Second

func (d *Daemon) moveSessionToRoot(ctx context.Context, request sessionMoveRequest) (sessionMoveResult, error) {
	session, err := d.Registry.Session(request.SessionID)
	if err != nil {
		return sessionMoveResult{}, MoveRefusedError{Reason: fmt.Sprintf("session %s not found", request.SessionID)}
	}
	if reason, err := d.Registry.SessionMoveRefusal(session.ID); err != nil {
		return sessionMoveResult{}, err
	} else if reason != "" {
		return sessionMoveResult{}, MoveRefusedError{Reason: fmt.Sprintf("session %s cannot move: %s", session.ID, reason)}
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		return sessionMoveResult{}, err
	}
	mover, movable := adapter.(surface.SessionMover)
	rootMover, rootMovable := adapter.(surface.RootMover)
	if !movable || !rootMovable {
		return sessionMoveResult{}, MoveRefusedError{Reason: fmt.Sprintf("%s sessions cannot move between accounts", session.Surface)}
	}
	out, err := mover.PrepareMove(ctx, session, surface.AdoptRequest{StopTerminal: request.StopTerminal, Wait: time.Duration(request.WaitSeconds) * time.Second})
	if err != nil {
		return sessionMoveResult{}, err
	}
	defer out.Release()
	alias, _ := d.Registry.ReverseAlias(session.ID)
	self := d.hostIdentity().ID
	manifest := moveManifest{ID: uuid.NewString(), Conversation: session.ID, SourceHost: self, Surface: session.Surface, Alias: alias, Title: out.Title, Model: out.Model, PermissionMode: out.PermissionMode, Size: out.Size, SHA256: out.SHA256, PartsSize: out.PartsSize, Root: request.Root}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return sessionMoveResult{}, err
	}
	release, _ := claimMove(manifest.ID)
	defer release()
	if err := d.Registry.BeginMoveOut(manifest.ID, session.ID, self, encoded); err != nil {
		return sessionMoveResult{}, MoveRefusedError{Reason: err.Error()}
	}
	result := sessionMoveResult{Host: d.localHost(), Stopped: out.Stopped, Alias: alias, Root: request.Root}
	resumed, err := rootMover.MoveToRoot(ctx, out, manifest.ID, request.Root)
	switch {
	case errors.Is(err, surface.ErrRootMoveUnsettled):
		result.Move, _ = d.Registry.SessionMove(manifest.ID)
		return result, fmt.Errorf("move %s of %s is unsettled; it settles when the daemon next starts: %w", manifest.ID, session.ID, err)
	case err != nil && !errors.Is(err, surface.ErrMoveInPlaced):
		_ = d.Registry.SetMoveState(manifest.ID, registry.MoveFailed, err.Error(), registry.MovePreparing)
		result.Move, _ = d.Registry.SessionMove(manifest.ID)
		return result, MoveRefusedError{Reason: fmt.Sprintf("session %s did not move to %s: %s", session.ID, request.Root, err)}
	}
	detail := ""
	if err != nil {
		detail = "its resume under the new account is unconfirmed: " + err.Error()
	}
	if err := d.Registry.SetMoveState(manifest.ID, registry.MoveDone, detail, registry.MovePreparing); err != nil {
		return result, err
	}
	if resumed != nil {
		if err := d.Registry.RegisterSession(*resumed); err != nil {
			d.logRuntimeError("move "+manifest.ID+" register", err)
		}
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "session-operation", SessionID: session.ID, Result: "moved to account " + request.Root})
	d.releaseMovedLimitStop(session.ID)
	d.settledRootMove()
	result.Move, _ = d.Registry.SessionMove(manifest.ID)
	return result, nil
}

func localRootMove(move registry.SessionMove) (moveManifest, bool) {
	var manifest moveManifest
	if json.Unmarshal(move.Manifest, &manifest) != nil {
		return manifest, false
	}
	return manifest, manifest.Root != "" && move.PeerHost == manifest.SourceHost
}

func (d *Daemon) recoverRootMove(move registry.SessionMove, manifest moveManifest) {
	mover, ok := d.adapters().ForSurface(manifest.Surface).(surface.RootMover)
	if !ok {
		_ = d.Registry.SetMoveState(move.ID, registry.MoveFailed, "no adapter can settle the move", registry.MovePreparing)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), rootMoveRecoverTimeout)
	defer cancel()
	moved, err := mover.RecoverRootMove(ctx, move.Conversation, move.ID, manifest.Root)
	if err != nil {
		d.log.Printf("move %s: %s", move.ID, err)
		return
	}
	state, detail := registry.MoveFailed, "the daemon stopped before the move finished"
	if moved {
		state, detail = registry.MoveDone, "the daemon stopped during the move; it finished when the daemon started"
	}
	if err := d.Registry.SetMoveState(move.ID, state, detail, registry.MovePreparing); err != nil {
		d.log.Printf("move %s: %s", move.ID, err)
	}
	d.settledRootMove()
}

func (d *Daemon) settledRootMove() {
	if d.dashboard != nil {
		d.dashboard.invalidate()
	}
	d.requestCatalogDiscovery()
}
