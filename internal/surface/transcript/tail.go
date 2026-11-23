package transcript

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// maxJSONLRecordBytes bounds every transcript line read by shared JSONL readers.
const maxJSONLRecordBytes = 32 * 1024 * 1024

type Cursor struct {
	Identity string
	Offset   int64
}

func (c *Cursor) Reset(info os.FileInfo) bool {
	identity := FileIdentity(info)
	if c.Identity == identity && identity != "" && info.Size() >= c.Offset {
		return false
	}
	c.Identity, c.Offset = identity, 0
	return true
}

func ReadFirstJSONL(path string, value any) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	return DecodeFirstJSONL(file, value)
}

func DecodeFirstJSONL(source io.Reader, value any) (int64, error) {
	reader := bufio.NewReaderSize(source, 64*1024)
	var line []byte
	for {
		part, readErr := reader.ReadSlice('\n')
		if len(line)+len(part) > maxJSONLRecordBytes {
			return 0, RecordTooLarge{limit: maxJSONLRecordBytes}
		}
		line = append(line, part...)
		if readErr != bufio.ErrBufferFull {
			if readErr != nil && readErr != io.EOF {
				return 0, readErr
			}
			break
		}
	}
	return int64(len(line)), json.Unmarshal(line, value)
}

func FindJSONL(path string, visit func([]byte) (bool, error)) error {
	return FindJSONLFrom(path, 0, visit)
}

func FindJSONLFrom(path string, offset int64, visit func([]byte) (bool, error)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = scanJSONL(context.Background(), file, offset, maxJSONLRecordBytes, true, func(line []byte) error {
		found, err := visit(line)
		if found && err == nil {
			return io.EOF
		}
		return err
	})
	if err == io.EOF {
		return nil
	}
	return err
}

func ReadRecentJSONLLines(ctx context.Context, path string, limit int, byteLimit int64, recordLimit int) ([][]byte, int64, error) {
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

type RecordTooLarge struct{ limit int }

func (e RecordTooLarge) Error() string {
	return fmt.Sprintf("transcript record exceeds %d bytes", e.limit)
}

func ScanAppendedJSONL(ctx context.Context, path string, offset int64, limit int, visit func([]byte) error) (int64, error) {
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
	if info.Size() == offset {
		return offset, nil
	}
	return scanJSONL(ctx, file, offset, limit, false, visit)
}

// ScanJSONL visits every line of a JSONL stream, the last one even without
// its newline.
func ScanJSONL(ctx context.Context, source io.Reader, limit int, visit func([]byte) error) error {
	_, err := scanJSONLFrom(ctx, source, 0, limit, true, visit)
	return err
}

func scanJSONL(ctx context.Context, file *os.File, offset int64, limit int, final bool, visit func([]byte) error) (int64, error) {
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}
	return scanJSONLFrom(ctx, file, offset, limit, final, visit)
}

func scanJSONLFrom(ctx context.Context, source io.Reader, offset int64, limit int, final bool, visit func([]byte) error) (int64, error) {
	reader := bufio.NewReaderSize(source, 64*1024)
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
				return current, RecordTooLarge{limit: limit}
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
		if readErr == io.EOF && len(line) > 0 && !final {
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
