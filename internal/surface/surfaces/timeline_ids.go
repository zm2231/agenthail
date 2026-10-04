package surfaces

import (
	"crypto/sha256"
	"fmt"
)

func stableTimelineItemID(offset int64, line []byte, index int) string {
	digest := fmt.Sprintf("%x", sha256.Sum256(line))[:12]
	return fmt.Sprintf("%d-%s-%d", offset, digest, index)
}
