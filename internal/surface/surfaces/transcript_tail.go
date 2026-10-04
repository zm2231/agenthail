package surfaces

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func codexTranscriptUsesEventUsers(ctx context.Context, path string, end int64) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	if end == 0 {
		info, statErr := file.Stat()
		if statErr != nil {
			return false, statErr
		}
		end = info.Size()
	}
	reader := bufio.NewReaderSize(file, 64*1024)
	var offset int64
	for offset < end {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		var line []byte
		for {
			part, readErr := reader.ReadSlice('\n')
			if int64(len(line)+len(part)) > maxCodexTranscriptRecordBytes {
				return false, fmt.Errorf("transcript record exceeds %d bytes", maxCodexTranscriptRecordBytes)
			}
			line = append(line, part...)
			if readErr != bufio.ErrBufferFull {
				if readErr != nil && readErr != io.EOF {
					return false, readErr
				}
				break
			}
		}
		if len(line) == 0 {
			break
		}
		offset += int64(len(line))
		if offset > end {
			break
		}
		var record map[string]any
		if json.Unmarshal(line, &record) == nil {
			payload, _ := record["payload"].(map[string]any)
			if str(record, "type") == "event_msg" && str(payload, "type") == "user_message" {
				return true, nil
			}
		}
		if len(line) == 0 || offset >= end {
			break
		}
	}
	return false, nil
}

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
		var line []byte
		var readErr error
		for {
			part, partErr := reader.ReadSlice('\n')
			if len(line)+len(part) > limit {
				return current, fmt.Errorf("transcript record exceeds %d bytes", limit)
			}
			line = append(line, part...)
			readErr = partErr
			if partErr != bufio.ErrBufferFull {
				break
			}
			if err := ctx.Err(); err != nil {
				return current, err
			}
		}
		if readErr == io.EOF && len(line) > 0 {
			return current, nil
		}
		if readErr != nil && readErr != io.EOF {
			return current, readErr
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
