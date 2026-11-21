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
		var format transcriptFormat = claudeTranscript{settled: func(time.Time) bool { return true }}
		if strings.Contains(filepath.Base(path), "rollout") {
			format = codexTranscript{}
		}
		b.Run(filepath.Base(path), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				page, err := readTranscriptPage(context.Background(), path, format, 0, 40)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(len(page.Items)), "items")
			}
		})
	}
}
