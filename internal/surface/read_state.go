package surface

import (
	"context"
	"fmt"
)

type ReadState struct {
	FirstUnread string `json:"firstUnread,omitempty"`
	Revision    string `json:"revision"`
}

const ReadFromStart = "threadStart"

func (s ReadState) Unread() bool { return s.FirstUnread != "" }

type ReadStateWriter interface {
	SetReadState(ctx context.Context, sess *Session, unread bool, revision string) (ReadState, error)
}

// Never retried: only the person can judge the newer receipt.
type ReadStateConflict struct {
	Current ReadState
}

func (e *ReadStateConflict) Error() string {
	return fmt.Sprintf("the conversation's read state changed (now revision %s); look again before marking it", e.Current.Revision)
}
