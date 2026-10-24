package daemon

import (
	"bytes"
	"encoding/json"
	"time"
)

const apiTimestampLayout = "2006-01-02T15:04:05.000000000Z"

var apiTimestampFields = map[string]struct{}{
	"at": {}, "askedAt": {}, "archivedAt": {}, "busySince": {}, "capturedAt": {}, "changedAt": {}, "completedAt": {}, "createdAt": {}, "deliveredAt": {}, "expiresAt": {}, "fetchedAt": {}, "lastActive": {}, "lastCompactedAt": {}, "lastContactAt": {}, "lastFiredAt": {}, "lastProgressAt": {}, "lastSeenAt": {}, "observedAt": {}, "queuedAt": {}, "refusedAt": {}, "registeredAt": {}, "rekeyedAt": {}, "resolvedAt": {}, "resetsAt": {}, "revokedAt": {}, "setAt": {}, "settledAt": {}, "since": {}, "startedAt": {}, "timestamp": {}, "until": {}, "updatedAt": {},
	"archived_at": {}, "captured_at": {}, "completed_at": {}, "created_at": {}, "delivered_at": {}, "expires_at": {}, "last_seen_at": {}, "observed_at": {}, "queued_at": {}, "resets_at": {}, "revoked_at": {}, "timestamp_at": {}, "ts": {}, "updated_at": {},
}

func marshalAPIJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return json.Marshal(normalizeAPIValue(decoded, ""))
}

func normalizeAPIValue(value any, field string) any {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			value[key] = normalizeAPIValue(item, key)
		}
		return value
	case []any:
		for index, item := range value {
			value[index] = normalizeAPIValue(item, field)
		}
		return value
	case string:
		if _, ok := apiTimestampFields[field]; !ok {
			return value
		}
		return normalizeAPITimestamp(value)
	default:
		return value
	}
}

func normalizeAPITimestamp(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		parsed, err = time.ParseInLocation(time.DateTime, value, time.UTC)
		if err != nil {
			return value
		}
	}
	return parsed.UTC().Format(apiTimestampLayout)
}
