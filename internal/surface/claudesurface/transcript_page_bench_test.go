package claudesurface

import (
	"context"
	"github.com/zm2231/agenthail/internal/surface/transcript"
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
		if strings.Contains(filepath.Base(path), "rollout") {
			continue
		}
		var format transcript.Format = claudeTranscript{settled: func(time.Time) bool { return true }}
		b.Run(filepath.Base(path), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				page, err := transcript.ReadPage(context.Background(), path, format, 0, 40)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(len(page.Items)), "items")
			}
		})
	}
}
