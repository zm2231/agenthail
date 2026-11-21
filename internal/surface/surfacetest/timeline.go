package surfacetest

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

const TestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func ReadTimeline(ctx context.Context, reader surface.SessionReader, path string, before int64, limit int) (*surface.SessionReadResult, error) {
	return reader.ReadSession(ctx, &surface.Session{Transcript: path}, surface.SessionReadRequest{Before: before, Limit: limit})
}

func FirstAttachmentID(t *testing.T, reader surface.SessionReader, path string) string {
	t.Helper()
	page, err := ReadTimeline(context.Background(), reader, path, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.Kind == "attachment" && item.Attachment != nil {
			return item.Attachment.ID
		}
	}
	t.Fatalf("no attachment in %+v", page.Items)
	return ""
}

func AttachmentData(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(TestPNG)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TimelineSummary(items []surface.TimelineItem) []string {
	summary := make([]string, 0, len(items))
	for _, item := range items {
		entry := item.Kind + ":" + item.Role + ":" + item.Text
		if item.Kind == "attachment" && item.Attachment == nil {
			entry += ":unresolved"
		}
		summary = append(summary, entry)
	}
	return summary
}
