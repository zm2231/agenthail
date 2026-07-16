package surfaces

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
)

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
