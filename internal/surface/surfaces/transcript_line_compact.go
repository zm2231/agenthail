package surfaces

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

const (
	compactLineBytes   = 1 << 20
	compactStringBytes = 64 << 10
	compactCutField    = "agenthailCut"
	toolOutputLimit    = "tool output limit"
)

type lineEdit struct {
	start, end  int
	replacement string
}

func decodeCompactRecord(line []byte, record *map[string]any) error {
	return json.Unmarshal(compactTranscriptLine(line), record)
}

func compactTranscriptLine(line []byte) []byte {
	if len(line) <= compactLineBytes {
		return line
	}
	text := string(line)
	var edits []lineEdit
	edits = cutLongStrings(edits, gjson.Get(text, "toolUseResult"))
	message := gjson.Get(text, "message")
	content := message.Get("content")
	if content.Type == gjson.String && message.Get("tool_use_id").Exists() {
		if cut := cutLongStrings(nil, content); len(cut) > 0 {
			edits = append(append(edits, flagCut(message)), cut...)
		}
	} else if content.IsArray() {
		content.ForEach(func(_, block gjson.Result) bool {
			if block.Get("type").Str == "tool_result" {
				if cut := cutLongStrings(nil, block.Get("content")); len(cut) > 0 {
					edits = append(append(edits, flagCut(block)), cut...)
				}
			}
			return true
		})
	}
	if len(edits) == 0 {
		return line
	}
	slices.SortStableFunc(edits, func(a, b lineEdit) int { return cmp.Compare(a.start, b.start) })
	var out strings.Builder
	at := 0
	for _, edit := range edits {
		out.WriteString(text[at:edit.start])
		out.WriteString(edit.replacement)
		at = edit.end
	}
	out.WriteString(text[at:])
	return []byte(out.String())
}

func flagCut(object gjson.Result) lineEdit {
	at := object.Index + 1
	return lineEdit{start: at, end: at, replacement: `"` + compactCutField + `":true,`}
}

func cutLongStrings(edits []lineEdit, value gjson.Result) []lineEdit {
	switch {
	case value.Index <= 0:
	case value.Type == gjson.String && len(value.Raw) > compactStringBytes+2:
		kept := jsonStringPrefix(value.Raw[1:len(value.Raw)-1], compactStringBytes)
		edits = append(edits, lineEdit{start: value.Index, end: value.Index + len(value.Raw), replacement: `"` + kept + `"`})
	case value.IsObject() && value.Get("type").Str == "image":
	case value.IsObject() || value.IsArray():
		value.ForEach(func(_, child gjson.Result) bool {
			edits = cutLongStrings(edits, child)
			return true
		})
	}
	return edits
}

func jsonStringPrefix(raw string, limit int) string {
	at := 0
	for at < len(raw) {
		step := 1
		switch {
		case raw[at] == '\\' && at+1 < len(raw) && raw[at+1] == 'u':
			step = 6
		case raw[at] == '\\':
			step = 2
		default:
			_, step = utf8.DecodeRuneInString(raw[at:])
		}
		if at+step > limit {
			break
		}
		at += step
	}
	return raw[:at]
}

func recordCut(object map[string]any) bool {
	cut, _ := object[compactCutField].(bool)
	return cut
}
