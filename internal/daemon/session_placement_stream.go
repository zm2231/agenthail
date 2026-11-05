package daemon

import "encoding/json"

func withPlacement(payload json.RawMessage) (json.RawMessage, error) {
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
	fields["placement"] = placement
	if envelope["session"], err = json.Marshal(fields); err != nil {
		return nil, err
	}
	return json.Marshal(envelope)
}
