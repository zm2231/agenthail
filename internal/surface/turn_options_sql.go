package surface

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

func (o TurnOptions) Value() (driver.Value, error) {
	data, err := json.Marshal(o)
	return string(data), err
}
func (o *TurnOptions) Scan(value any) error {
	data, err := storedJSON(value, "turn options")
	if err != nil {
		return err
	}
	*o = TurnOptions{}
	return json.Unmarshal(data, o)
}

func storedJSON(value any, what string) ([]byte, error) {
	switch v := value.(type) {
	case string:
		return []byte(v), nil
	case []byte:
		return v, nil
	default:
		return nil, fmt.Errorf("invalid stored %s: %T", what, value)
	}
}
