package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
)

var errPlacementPayload = errors.New("session.upserted payload has no session object")

func presentedUpsert(payload []byte, presenter rowPresenter) ([]byte, error) {
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
	presenter.present(&row, localSessionID(row.ID))
	members := []struct {
		key   string
		value any
		set   bool
	}{
		{"placement", sessionPlacement(row), true},
		{"display", row.Display, row.Display != nil},
		{"attention", row.Attention, row.Attention != nil},
		{"replacedLead", row.ReplacedLead, row.ReplacedLead != nil},
	}
	for _, member := range members {
		var err error
		if payload, err = spliceSessionMember(payload, member.key, member.value, member.set); err != nil {
			return nil, err
		}
	}
	return payload, nil
}

func spliceSessionMember(payload []byte, key string, value any, set bool) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if !set {
		encoded = []byte("null")
	}
	at, stop, _, err := topLevelValue(payload, "session")
	if err != nil || at < 0 {
		return nil, errPlacementPayload
	}
	raw := payload[at:stop]
	start, end, members, err := topLevelValue(raw, key)
	if err != nil {
		return nil, err
	}
	insert := encoded
	if start < 0 {
		if !set {
			return payload, nil
		}
		start = bytes.LastIndexByte(raw, '}')
		end = start
		name, _ := json.Marshal(key)
		insert = append(append(name, ':'), encoded...)
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
