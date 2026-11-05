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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	var row dashboardSession
	if err := json.Unmarshal(raw, &row); err != nil {
		return nil, err
	}
	placement, err := json.Marshal(sessionPlacement(row))
	if err != nil {
		return nil, err
	}
	if _, stored := fields["placement"]; stored {
		fields["placement"] = placement
		if envelope["session"], err = json.Marshal(fields); err != nil {
			return nil, err
		}
		return json.Marshal(envelope)
	}
	at := bytes.Index(payload, raw)
	end := bytes.LastIndexByte(raw, '}')
	if at < 0 || end < 0 {
		return nil, errPlacementPayload
	}
	member := append([]byte(`"placement":`), placement...)
	if len(fields) > 0 {
		member = append([]byte(","), member...)
	}
	spliced := make([]byte, 0, len(payload)+len(member))
	spliced = append(spliced, payload[:at+end]...)
	spliced = append(spliced, member...)
	return append(spliced, payload[at+end:]...), nil
}
