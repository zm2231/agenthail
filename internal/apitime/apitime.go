package apitime

import (
	"bytes"
	"encoding/json"
)

func Marshal(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return Normalize(encoded), nil
}

// frame is one open object or array. An array's strings take the field
// that holds the array, so a list of timestamps is normalized like one.
type frame struct {
	object bool
	key    bool
	field  []byte
}

func Normalize(encoded []byte) []byte {
	var output []byte
	copied := 0
	frames := make([]frame, 0, 16)
	for index := 0; index < len(encoded); index++ {
		switch encoded[index] {
		case '{':
			frames = append(frames, frame{object: true, key: true})
		case '[':
			var field []byte
			if len(frames) > 0 {
				field = frames[len(frames)-1].field
			}
			frames = append(frames, frame{field: field})
		case '}', ']':
			frames = frames[:len(frames)-1]
		case ',':
			if top := len(frames) - 1; top >= 0 && frames[top].object {
				frames[top].key = true
			}
		case '"':
			end, escaped := stringEnd(encoded, index)
			if len(frames) == 0 {
				index = end
				continue
			}
			top := &frames[len(frames)-1]
			if top.object && top.key {
				top.field, top.key = stringText(encoded[index:end+1], escaped), false
				index = end
				continue
			}
			if _, ok := fields[string(top.field)]; ok {
				text := string(stringText(encoded[index:end+1], escaped))
				if normalized := normalize(text); normalized != text {
					if output == nil {
						output = make([]byte, 0, len(encoded)+len(encoded)/4)
					}
					output = append(output, encoded[copied:index]...)
					output = append(output, '"')
					output = append(output, normalized...)
					output = append(output, '"')
					copied = end + 1
				}
			}
			index = end
		}
	}
	if output == nil {
		return encoded
	}
	return append(output, encoded[copied:]...)
}

func stringEnd(encoded []byte, start int) (int, bool) {
	escaped := false
	for index := start + 1; ; {
		quote := index + bytes.IndexByte(encoded[index:], '"')
		backslash := bytes.IndexByte(encoded[index:quote], '\\')
		if backslash < 0 {
			return quote, escaped
		}
		escaped = true
		index += backslash + 2
	}
}

func stringText(raw []byte, escaped bool) []byte {
	if !escaped {
		return raw[1 : len(raw)-1]
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return nil
	}
	return []byte(text)
}
