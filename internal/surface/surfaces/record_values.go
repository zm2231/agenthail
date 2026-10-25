package surfaces

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func strNested(record map[string]any, object, field string) string {
	value, _ := record[object].(map[string]any)
	return str(value, field)
}
