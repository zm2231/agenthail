package surfaces

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
)

func readRecentJSONLLines(ctx context.Context, path string, limit int, byteLimit int64, recordLimit int) ([][]byte, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	end := info.Size()
	if end == 0 {
		return nil, end, nil
	}
	minimum := int64(0)
	if byteLimit > 0 && end > byteLimit {
		minimum = end - byteLimit
	}
	buffer := make([]byte, end-minimum)
	if _, err := file.ReadAt(buffer, minimum); err != nil && err != io.EOF {
		return nil, 0, err
	}
	if last := bytes.LastIndexByte(buffer, '\n'); last >= 0 && last+1 < len(buffer) {
		buffer = buffer[:last+1]
		end = minimum + int64(len(buffer))
	} else if len(buffer) > 0 && buffer[len(buffer)-1] != '\n' {
		return nil, minimum, nil
	}
	if minimum > 0 {
		first := bytes.IndexByte(buffer, '\n')
		if first < 0 {
			return nil, end, nil
		}
		buffer = buffer[first+1:]
	}
	lines := make([][]byte, 0, limit)
	lineEnd := len(buffer)
	for index := len(buffer) - 1; index >= 0 && (limit <= 0 || len(lines) < limit); index-- {
		if err := ctx.Err(); err != nil {
			return nil, end, err
		}
		if buffer[index] != '\n' {
			continue
		}
		if index+1 < lineEnd {
			line := buffer[index+1 : lineEnd]
			if len(line) > recordLimit {
				return nil, end, fmt.Errorf("transcript record exceeds %d bytes", recordLimit)
			}
			lines = append(lines, append([]byte(nil), line...))
		}
		lineEnd = index
	}
	if minimum == 0 && lineEnd > 0 && (limit <= 0 || len(lines) < limit) {
		line := buffer[:lineEnd]
		if len(line) > recordLimit {
			return nil, end, fmt.Errorf("transcript record exceeds %d bytes", recordLimit)
		}
		lines = append(lines, append([]byte(nil), line...))
	}
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	return lines, end, nil
}

func scanAppendedJSONL(ctx context.Context, path string, offset int64, limit int, visit func([]byte) error) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return offset, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return offset, err
	}
	if info.Size() < offset {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}
	reader := bufio.NewReaderSize(file, 64*1024)
	current := offset
	for {
		if err := ctx.Err(); err != nil {
			return current, err
		}
		line, readErr := reader.ReadBytes('\n')
		if readErr == io.EOF && len(line) > limit {
			return current, fmt.Errorf("transcript record exceeds %d bytes", limit)
		}
		if readErr == io.EOF && len(line) > 0 {
			return current, nil
		}
		if readErr != nil && readErr != io.EOF {
			return current, readErr
		}
		if len(line) > limit {
			return current, fmt.Errorf("transcript record exceeds %d bytes", limit)
		}
		if len(line) > 1 {
			if err := visit(line); err != nil {
				return current, err
			}
		}
		current += int64(len(line))
		if readErr == io.EOF {
			return current, nil
		}
	}
}
