package codexsurface

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func BenchmarkCodexTranscriptNewestPage(b *testing.B) {
	paths := os.Getenv("AGENTHAIL_TRANSCRIPT_BENCH")
	if paths == "" {
		b.Skip("AGENTHAIL_TRANSCRIPT_BENCH names the transcripts to read")
	}
	for _, path := range strings.Split(paths, ",") {
		if !strings.Contains(filepath.Base(path), "rollout") {
			continue
		}
		var format transcript.Format = codexTranscript{}
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
