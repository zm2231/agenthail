package daemon

import (
	"encoding/json"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	sessionTranscriptJSONBudget = 512 << 10
	sessionTranscriptFieldLimit = 64 << 10
)

type sessionTranscriptMetadata struct {
	Truncated         bool
	OriginalBytes     int
	ReturnedBytes     int
	OriginalExchanges int
}

func truncateSessionExchanges(exchanges []surface.Exchange) ([]surface.Exchange, sessionTranscriptMetadata) {
	metadata := sessionTranscriptMetadata{OriginalExchanges: len(exchanges)}
	for _, exchange := range exchanges {
		metadata.OriginalBytes += len(exchange.User) + len(exchange.Assistant)
	}
	remaining := sessionTranscriptJSONBudget
	result := make([]surface.Exchange, len(exchanges))
	for index, exchange := range exchanges {
		share := remaining / (len(exchanges) - index)
		userBudget := min(sessionTranscriptFieldLimit, share)
		if exchange.User != "" && exchange.Assistant != "" {
			userBudget = min(userBudget, share/2)
		}
		user, userEncoded, userTruncated := truncateJSONText(exchange.User, userBudget)
		assistant, assistantEncoded, assistantTruncated := truncateJSONText(exchange.Assistant, min(sessionTranscriptFieldLimit, share-userEncoded))
		remaining -= userEncoded + assistantEncoded
		exchange.User = user
		exchange.Assistant = assistant
		metadata.ReturnedBytes += len(user) + len(assistant)
		metadata.Truncated = metadata.Truncated || userTruncated || assistantTruncated
		result[index] = exchange
	}
	return result, metadata
}

func truncateJSONText(value string, budget int) (string, int, bool) {
	if value == "" || budget <= 0 {
		return "", 0, value != ""
	}
	candidate := value
	truncated := false
	if len(candidate) > budget {
		candidate = validUTF8Prefix(candidate, budget)
		truncated = true
	}
	encoded, _ := json.Marshal(candidate)
	encodedBytes := max(0, len(encoded)-2)
	if encodedBytes <= budget {
		return candidate, encodedBytes, truncated
	}
	candidate = validUTF8Prefix(candidate, budget/6)
	encoded, _ = json.Marshal(candidate)
	return candidate, max(0, len(encoded)-2), true
}

func validUTF8Prefix(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	if limit <= 0 {
		return ""
	}
	for limit > 0 && (value[limit]&0xc0) == 0x80 {
		limit--
	}
	return value[:limit]
}
