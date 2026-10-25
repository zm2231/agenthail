package daemon

import (
	"encoding/json"

	"github.com/tidwall/gjson"
)

func marshalAPIJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var output []byte
	copied := 0
	var visit func(gjson.Result, string)
	visit = func(value gjson.Result, field string) {
		if value.Type == gjson.JSON {
			object := value.IsObject()
			value.ForEach(func(key, item gjson.Result) bool {
				name := field
				if object {
					name = key.Str
				}
				visit(item, name)
				return true
			})
			return
		}
		if value.Type != gjson.String {
			return
		}
		if _, ok := apiTimestampFields[field]; !ok {
			return
		}
		normalized := normalizeAPITimestamp(value.Str)
		if normalized == value.Str {
			return
		}
		if output == nil {
			output = make([]byte, 0, len(encoded))
		}
		output = append(output, encoded[copied:value.Index]...)
		output = append(output, '"')
		output = append(output, normalized...)
		output = append(output, '"')
		copied = value.Index + len(value.Raw)
	}
	visit(gjson.ParseBytes(encoded), "")
	if output == nil {
		return encoded, nil
	}
	return append(output, encoded[copied:]...), nil
}
