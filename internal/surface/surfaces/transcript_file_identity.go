package surfaces

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
)

func completeJSONLEnd(ctx context.Context, file *os.File, end int64) (int64, error) {
	if end == 0 {
		return 0, nil
	}
	const chunkSize int64 = 64 << 10
	remaining := int64(maxCodexTranscriptRecordBytes)
	position := end
	for position > 0 && remaining > 0 {
		readSize := minInt64(chunkSize, position)
		if readSize > remaining {
			readSize = remaining
		}
		start := position - readSize
		data := make([]byte, readSize)
		if _, err := file.ReadAt(data, start); err != nil && err != io.EOF {
			return 0, err
		}
		if index := bytes.LastIndexByte(data, '\n'); index >= 0 {
			return start + int64(index+1), nil
		}
		position = start
		remaining -= readSize
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
	}
	return 0, nil
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func transcriptFileIdentity(info os.FileInfo) string {
	if info == nil || info.Sys() == nil {
		return ""
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.IsValid() && value.Kind() == reflect.Struct {
		dev := value.FieldByName("Dev")
		ino := value.FieldByName("Ino")
		if dev.IsValid() && ino.IsValid() && dev.CanInterface() && ino.CanInterface() {
			return fmt.Sprintf("%v:%v", dev.Interface(), ino.Interface())
		}
	}
	return ""
}
