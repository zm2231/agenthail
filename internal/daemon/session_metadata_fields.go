package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	sessionMetadataTimeout = 3 * time.Second
	metadataReasonLimit    = 160
)

func metadataTimedOut() string {
	return fmt.Sprintf("Timed out after %s.", sessionMetadataTimeout)
}

func metadataFailure(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return metadataTimedOut()
	case errors.Is(err, surface.ErrUnsupported):
		return "Not supported for this session."
	}
	reason, _, _ := strings.Cut(strings.TrimSpace(err.Error()), "\n")
	if utf8.RuneCountInString(reason) > metadataReasonLimit {
		reason = string([]rune(reason)[:metadataReasonLimit-1]) + "…"
	}
	return "Could not read: " + reason
}
