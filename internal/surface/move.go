package surface

import (
	"context"
	"io"
)

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
	ArchiveMove(conversation string) error
	RestoreMove(conversation string) error
	WriteMoveArchive(w io.Writer, conversation string) error
	CheckMoveIn(in MoveIn) error
	ReceiveMove(ctx context.Context, in MoveIn, archive io.Reader) (*Session, error)
	// MoveInRunning reports whether a process here holds the conversation, so
	// a move that brought it is no longer abandoned.
	MoveInRunning(ctx context.Context, conversation string) (bool, error)
}
