package hosts

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

type SigningKey struct {
	KeyID     string            `json:"keyId"`
	PublicKey ed25519.PublicKey `json:"publicKey"`
	CreatedAt time.Time         `json:"createdAt"`
	RetiredAt time.Time         `json:"retiredAt,omitzero"`
	RevokedAt time.Time         `json:"revokedAt,omitzero"`
}

func SigningKeyID(public ed25519.PublicKey) string {
	sum := sha256.Sum256(public)
	return hex.EncodeToString(sum[:16])
}

func (k SigningKey) Valid() bool {
	return len(k.PublicKey) == ed25519.PublicKeySize && SigningKeyID(k.PublicKey) == k.KeyID
}
