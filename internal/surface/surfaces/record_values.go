package surfaces

import "encoding/json"

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func strNested(record map[string]any, object, field string) string {
	value, _ := record[object].(map[string]any)
	return str(value, field)
}

func int64Value(value any) int64 {
	switch number := value.(type) {
	case int64:
		return number
	case int:
		return int64(number)
	case float64:
		return int64(number)
	case json.Number:
		parsed, _ := number.Int64()
		return parsed
	default:
		return 0
	}
}
