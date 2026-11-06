package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func BenchmarkTranscriptNewestPage(b *testing.B) {
	paths := os.Getenv("AGENTHAIL_TRANSCRIPT_BENCH")
	if paths == "" {
		b.Skip("AGENTHAIL_TRANSCRIPT_BENCH names the transcripts to read")
	}
	for _, path := range strings.Split(paths, ",") {
		source := "claude"
		if strings.Contains(filepath.Base(path), "rollout") {
			source = "codex"
		}
		b.Run(filepath.Base(path), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				page, err := readTranscriptPage(context.Background(), path, source, 0, 40, func(time.Time) bool { return true })
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(len(page.Items)), "items")
			}
		})
	}
}
