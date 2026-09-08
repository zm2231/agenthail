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
	var data []byte
	switch v := value.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		return fmt.Errorf("invalid stored turn options: %T", value)
	}
	*o = TurnOptions{}
	return json.Unmarshal(data, o)
}
