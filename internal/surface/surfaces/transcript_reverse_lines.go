package surfaces

import (
	"bytes"
	"context"
	"io"
	"os"
)

var reverseLineChunk int64 = 1 << 20

func reverseTranscriptLines(ctx context.Context, file *os.File, floor, offset int64, visit func(start int64, line []byte) bool) error {
	buf := make([]byte, reverseLineChunk)
	end := offset
	for end > floor {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := max(floor, end-reverseLineChunk)
		data := buf[:end-start]
		if _, err := file.ReadAt(data, start); err != nil && err != io.EOF {
			return err
		}
		next := len(data)
		for next > 0 {
			previous := bytes.LastIndexByte(data[:next-1], '\n')
			if previous < 0 {
				break
			}
			if !visit(start+int64(previous+1), data[previous+1:next]) {
				return nil
			}
			next = previous + 1
		}
		if start == 0 {
			visit(0, data[:next])
			return nil
		}
		if next < len(data) {
			end = start + int64(next)
			continue
		}
		lineStart, found, err := lineStartBefore(ctx, file, floor, start, buf)
		if err != nil || !found {
			return err
		}
		line := make([]byte, end-lineStart)
		if _, err := file.ReadAt(line, lineStart); err != nil && err != io.EOF {
			return err
		}
		if !visit(lineStart, line) {
			return nil
		}
		end = lineStart
	}
	return nil
}

func lineStartBefore(ctx context.Context, file *os.File, floor, offset int64, buf []byte) (int64, bool, error) {
	for end := offset; end > floor; {
		if err := ctx.Err(); err != nil {
			return 0, false, err
		}
		start := max(floor, end-int64(len(buf)))
		data := buf[:end-start]
		if _, err := file.ReadAt(data, start); err != nil && err != io.EOF {
			return 0, false, err
		}
		if index := bytes.LastIndexByte(data, '\n'); index >= 0 {
			return start + int64(index+1), true, nil
		}
		end = start
	}
	return 0, floor == 0, nil
}
