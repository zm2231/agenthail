package surface

import (
	"bytes"
	"encoding/json"
	"log"
	"strconv"
	"sync"
)

var loggedRecords sync.Map

// Claude rewrites the record in place, so a shorter one can leave a longer one's tail behind it.
func DecodeClaudeSessionRecord(path string, data []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(v); err != nil {
		logRecordOnce(path, data, "Claude session record %s cannot be read: %s", path, err)
		return err
	}
	if stray := len(bytes.TrimSpace(data[decoder.InputOffset():])); stray > 0 {
		logRecordOnce(path, data, "Claude session record %s has %d stray bytes after its record; reading the record before them", path, stray)
	}
	return nil
}

func logRecordOnce(path string, data []byte, format string, args ...any) {
	if _, logged := loggedRecords.LoadOrStore(path+"\x00"+strconv.Itoa(len(data)), true); !logged {
		log.Printf(format, args...)
	}
}
