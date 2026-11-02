package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

const truncationRoom = 64 << 10

func fitUtterance(p UtterancePayload) ([]byte, error) {
	data, err := json.Marshal(p)
	if err != nil || len(data) <= MaxEventBytes {
		return data, err
	}
	full := p.Text
	sum := sha256.Sum256([]byte(full))
	keep := len(full) * (MaxEventBytes - truncationRoom) / len(data)
	for {
		for keep > 0 && !utf8.RuneStart(full[keep]) {
			keep--
		}
		p.Truncated = &Truncation{OriginalBytes: len(full), KeptBytes: keep, SHA256: hex.EncodeToString(sum[:])}
		p.Text = full[:keep] + fmt.Sprintf("\n[agenthail: the person's message was %d bytes; the ledger keeps the first %d, sha256 %s]", len(full), keep, p.Truncated.SHA256)
		if data, err = json.Marshal(p); err != nil || len(data) <= MaxEventBytes {
			return data, err
		}
		keep -= (len(data) - MaxEventBytes) + truncationRoom
		if keep <= 0 {
			return nil, fmt.Errorf("the person's message cannot be kept even in part")
		}
	}
}
