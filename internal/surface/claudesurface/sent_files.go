package claudesurface

import (
	"context"
	"path/filepath"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

type claudeSentFile struct {
	path, mediaType string
	image           bool
}

func claudeSentFiles(record map[string]any) (callID, caption string, files []claudeSentFile) {
	if transcript.String(record, "type") != "user" {
		return "", "", nil
	}
	result, _ := record["toolUseResult"].(map[string]any)
	entries, _ := result["attachments"].([]any)
	if callID = claudeToolResultID(record); callID == "" || len(entries) == 0 {
		return "", "", nil
	}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		path := transcript.String(entry, "path")
		_, sized := entry["size"].(float64)
		image, hinted := entry["isImage"].(bool)
		if !filepath.IsAbs(path) || !sized || !hinted {
			return "", "", nil
		}
		files = append(files, claudeSentFile{path: path, mediaType: transcript.String(entry, "media_type"), image: image})
	}
	return callID, transcript.String(result, "caption"), files
}

func claudeToolResultID(record map[string]any) string {
	message, _ := record["message"].(map[string]any)
	if id := transcript.String(message, "tool_use_id"); id != "" {
		return id
	}
	blocks, _ := message["content"].([]any)
	for _, raw := range blocks {
		if block, _ := raw.(map[string]any); transcript.String(block, "type") == "tool_result" {
			return transcript.String(block, "tool_use_id")
		}
	}
	return ""
}

// verifiedSentFiles returns a result record's files only when its call is a
// SendUserFile tool_use earlier in the transcript: any tool's result can
// carry a toolUseResult of the same shape, and its paths must never be served.
func verifiedSentFiles(ctx context.Context, calls *claudeToolCalls, record map[string]any, offset int64) (string, []claudeSentFile, error) {
	callID, caption, files := claudeSentFiles(record)
	if len(files) == 0 {
		return "", nil, nil
	}
	name, err := calls.name(ctx, callID, offset)
	if err != nil || name != "SendUserFile" {
		return "", nil, err
	}
	return caption, files, nil
}

func sentFileReference(file claudeSentFile) transcript.AttachmentReference {
	return transcript.AttachmentReference{MediaType: file.mediaType, Path: file.path, Sent: true, Image: file.image}
}

func appendSentFiles(ctx context.Context, items []surface.TimelineItem, calls *claudeToolCalls, record map[string]any, offset int64, line []byte) ([]surface.TimelineItem, error) {
	caption, files, err := verifiedSentFiles(ctx, calls, record, offset)
	if err != nil || len(files) == 0 {
		return items, err
	}
	callID := claudeToolResultID(record)
	for i, file := range files {
		index := len(items)
		name := filepath.Base(file.path)
		item := surface.TimelineItem{ID: transcript.StableTimelineItemID(offset, line, index) + "-file", Kind: "file", Role: "assistant", Title: name, CallID: callID, Files: []surface.AttachedFile{{Name: name, Path: file.path}}}
		if i == 0 {
			item.Text = caption
		}
		if err := transcript.DecorateSentFile(ctx, &item, sentFileReference(file), offset, index); err != nil {
			return items, err
		}
		items = append(items, item)
	}
	return items, nil
}
