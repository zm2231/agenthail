package surface

import (
	"context"
	"errors"
	"io"
)

// ErrMoveInRunning is returned when a process here already holds a
// conversation a move brought, so the move can no longer be abandoned.
var ErrMoveInRunning = errors.New("the conversation already runs here")

// ErrMoveInPlaced is returned when a move's files may still be where the
// runtime finds them, so the outcome of the move-in is unknown.
var ErrMoveInPlaced = errors.New("the conversation's files may still be in place here")

// MoveOut is an idle conversation held for a move to another host. Its
// transcript stays unchanged until Release, which ends the hold.
type MoveOut struct {
	Conversation   string `json:"conversation"`
	Cwd            string `json:"cwd"`
	Title          string `json:"title,omitempty"`
	Model          string `json:"model,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
	// Stopped names the process the hold ended, as an adoption does.
	Stopped string `json:"stopped,omitempty"`
	Release func() `json:"-"`
}

// MoveIn is a conversation arriving from another host: where it runs here
// and what its transcript must be.
type MoveIn struct {
	MoveID         string
	Conversation   string
	Cwd            string
	Title          string
	Model          string
	PermissionMode string
	Size           int64
	SHA256         string
}

// SessionMover moves an idle conversation between machines. The source holds
// it with PrepareMove, takes its files out of the runtime's reach with
// ArchiveMove and streams them with WriteMoveArchive; RestoreMove puts them
// back when the move did not happen. The target checks it can take the
// conversation with CheckMoveIn and takes it with ReceiveMove.
type SessionMover interface {
	PrepareMove(ctx context.Context, sess *Session, request AdoptRequest) (*MoveOut, error)
	ArchiveMove(conversation, moveID string) error
	RestoreMove(conversation, moveID string) error
	WriteMoveArchive(w io.Writer, conversation, moveID string) error
	CheckMoveIn(in MoveIn) error
	// ReceiveMove runs admit while it holds the conversation, before it
	// places anything, so a move that admit finds aborted places nothing. A
	// failed move-in leaves no copy the runtime finds; when it cannot be sure
	// of that its error wraps ErrMoveInRunning or ErrMoveInPlaced.
	ReceiveMove(ctx context.Context, in MoveIn, archive io.Reader, admit func() error) (*Session, error)
	// DropMoveIn sets aside any copy the move placed here and runs commit
	// while it holds the conversation, so nothing resumes it in between. A
	// conversation a process here holds returns ErrMoveInRunning.
	DropMoveIn(ctx context.Context, conversation, moveID string, commit func() error) error
}
