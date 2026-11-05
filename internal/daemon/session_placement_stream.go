package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
)

var errPlacementPayload = errors.New("session.upserted payload has no session object")

func withPlacement(payload []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, err
	}
	raw, ok := envelope["session"]
	if !ok {
		return payload, nil
	}
	var row dashboardSession
	if err := json.Unmarshal(raw, &row); err != nil {
		return nil, err
	}
	placement, err := json.Marshal(sessionPlacement(row))
	if err != nil {
		return nil, err
	}
	at, _, _, err := topLevelValue(payload, "session")
	if err != nil || at < 0 {
		return nil, errPlacementPayload
	}
	start, end, members, err := topLevelValue(raw, "placement")
	if err != nil {
		return nil, err
	}
	insert := placement
	if start < 0 {
		start = bytes.LastIndexByte(raw, '}')
		end = start
		insert = append([]byte(`"placement":`), placement...)
		if members > 0 {
			insert = append([]byte(","), insert...)
		}
	}
	spliced := make([]byte, 0, len(payload)+len(insert))
	spliced = append(spliced, payload[:at+start]...)
	spliced = append(spliced, insert...)
	return append(spliced, payload[at+end:]...), nil
}

func topLevelValue(object []byte, key string) (start, end, members int, err error) {
	decoder := json.NewDecoder(bytes.NewReader(object))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return -1, -1, 0, errPlacementPayload
	}
	start, end = -1, -1
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return -1, -1, 0, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return -1, -1, 0, err
		}
		members++
		if name == key {
			end = int(decoder.InputOffset())
			start = end - len(value)
		}
	}
	return start, end, members, nil
}
