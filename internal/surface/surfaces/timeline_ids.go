package surfaces

import (
	"crypto/sha256"
	"fmt"
)

func stableTimelineItemID(offset int64, line []byte, index int) string {
	digest := sha256.Sum256(line)
	return fmt.Sprintf("%d-%x-%d", offset, digest[:6], index)
}
