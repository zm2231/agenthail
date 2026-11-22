package hosts

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Handover struct {
	Conversation string    `json:"conversation"`
	From         string    `json:"from"`
	To           string    `json:"to"`
	MoveID       string    `json:"moveId"`
	Seq          uint64    `json:"seq"`
	KeyID        string    `json:"keyId"`
	SignedAt     time.Time `json:"signedAt"`
	Signature    []byte    `json:"signature"`
}

const HandoversKept = 16

// The chain travels in a size-limited manifest.
func TrimHandovers(chain []Handover) []Handover {
	if len(chain) > HandoversKept {
		return chain[len(chain)-HandoversKept:]
	}
	return chain
}

func (h Handover) Statement() []byte {
	return []byte(strings.Join([]string{"agenthail handover v1", h.Conversation, h.From, h.To, h.MoveID, strconv.FormatUint(h.Seq, 10), h.KeyID, strconv.FormatInt(h.SignedAt.UnixMilli(), 10)}, "\n"))
}

func (h Handover) Verify(key SigningKey) error {
	switch {
	case h.Conversation == "" || h.From == "" || h.To == "" || h.From == h.To || h.MoveID == "" || h.Seq == 0:
		return errors.New("the handover is incomplete")
	case !key.Valid() || key.KeyID != h.KeyID:
		return fmt.Errorf("host %s published no key %s", h.From, h.KeyID)
	case !key.RevokedAt.IsZero():
		return fmt.Errorf("host %s revoked key %s", h.From, h.KeyID)
	case h.SignedAt.Before(key.CreatedAt) || !key.RetiredAt.IsZero() && !h.SignedAt.Before(key.RetiredAt):
		return fmt.Errorf("key %s was not host %s's signing key when the handover was signed", h.KeyID, h.From)
	case !ed25519.Verify(key.PublicKey, h.Statement(), h.Signature):
		return errors.New("the handover's signature does not match")
	}
	return nil
}
