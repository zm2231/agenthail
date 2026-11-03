package surfaces

import (
	"bytes"
	"io"
)

const jsonlBackwardChunk = int64(64 << 10)

// scanJSONLBackward visits the complete lines that end at or before end, newest
// first, reading fixed chunks and no more than limit bytes, until visit
// returns false. It returns how many bytes it read.
func scanJSONLBackward(file io.ReaderAt, end, limit int64, visit func(line []byte, offset int64) bool) int64 {
	var read int64
	var tail []byte
	for read < end && read < limit {
		chunk := min(jsonlBackwardChunk, end-read, limit-read)
		start := end - read - chunk
		part := make([]byte, chunk)
		if _, err := file.ReadAt(part, start); err != nil && err != io.EOF {
			return read + chunk
		}
		read += chunk
		tail = append(part, tail...)
		lines := bytes.Split(tail, []byte{'\n'})
		offsets := make([]int64, len(lines))
		at := start
		for i, line := range lines {
			offsets[i] = at
			at += int64(len(line)) + 1
		}
		first := 1
		if start == 0 {
			first = 0
		}
		for i := len(lines) - 1; i >= first; i-- {
			if len(bytes.TrimSpace(lines[i])) == 0 {
				continue
			}
			if !visit(lines[i], offsets[i]) {
				return read
			}
		}
		if start == 0 {
			break
		}
		if newline := bytes.IndexByte(tail, '\n'); newline >= 0 {
			tail = tail[:newline]
		}
	}
	return read
}
