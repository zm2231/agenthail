package surfaces

import (
	"context"
	"os"
	"runtime"
	"testing"
)

func TestAPollOfATranscriptThatDidNotGrowReadsNothing(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"u1","message":{"content":"one"}}`)
	end, err := scanAppendedJSONL(context.Background(), path, 0, maxClaudeTranscriptRecordBytes, func([]byte) error { return nil })
	if err != nil || end == 0 {
		t.Fatalf("first scan end=%d err=%v", end, err)
	}
	const polls = 100
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range polls {
		next, err := scanAppendedJSONL(context.Background(), path, end, maxClaudeTranscriptRecordBytes, func([]byte) error {
			t.Fatal("an unchanged transcript yielded a record")
			return nil
		})
		if err != nil || next != end {
			t.Fatalf("poll end=%d err=%v, want %d", next, err, end)
		}
	}
	runtime.ReadMemStats(&after)
	if perPoll := (after.TotalAlloc - before.TotalAlloc) / polls; perPoll >= 16*1024 {
		t.Fatalf("an unchanged poll allocated %d bytes", perPoll)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"type":"user","uuid":"u2","message":{"content":"two"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	records := 0
	if next, err := scanAppendedJSONL(context.Background(), path, end, maxClaudeTranscriptRecordBytes, func([]byte) error { records++; return nil }); err != nil || next <= end || records != 1 {
		t.Fatalf("after an append end=%d records=%d err=%v", next, records, err)
	}
}
