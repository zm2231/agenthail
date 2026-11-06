package surfaces

import (
	"bytes"
	"encoding/json"
)

func decodeShrunkRecord(line []byte, record *map[string]any) error {
	return json.Unmarshal(shrinkTranscriptLine(line), record)
}

func shrinkTranscriptLine(line []byte) []byte {
	if len(line) <= compactLineBytes {
		return line
	}
	var out []byte
	copied := 0
	for at := 0; at < len(line); {
		open := bytes.IndexByte(line[at:], '"')
		if open < 0 {
			break
		}
		open += at
		closing := jsonStringEnd(line, open+1)
		if closing < 0 {
			return line
		}
		if closing-open-1 > compactStringBytes {
			kept := jsonStringPrefix(string(line[open+1:min(closing, open+1+compactStringBytes+6)]), compactStringBytes)
			out = append(append(append(out, line[copied:open+1]...), kept...), '"')
			copied = closing + 1
		}
		at = closing + 1
	}
	if out == nil {
		return line
	}
	return append(out, line[copied:]...)
}

func jsonStringEnd(line []byte, from int) int {
	for at := from; at < len(line); {
		quote := bytes.IndexByte(line[at:], '"')
		if quote < 0 {
			return -1
		}
		quote += at
		escapes := 0
		for back := quote - 1; back >= from && line[back] == '\\'; back-- {
			escapes++
		}
		if escapes%2 == 0 {
			return quote
		}
		at = quote + 1
	}
	return -1
}
