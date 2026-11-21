package codexsurface

import (
	"context"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func (c *Codex) ReadAttachment(ctx context.Context, s *surface.Session, id string) (*surface.Attachment, []byte, error) {
	return transcript.ReadAttachment(ctx, s, id, codexTranscript{}, c.home.transcriptPath)
}

func codexAttachmentReferences(record map[string]any) []transcript.AttachmentReference {
	payload, _ := record["payload"].(map[string]any)
	var out []transcript.AttachmentReference
	if images, ok := payload["images"].([]any); ok {
		for _, v := range images {
			out = transcript.AppendAttachmentReference(out, v)
		}
	}
	blocks, _ := payload["content"].([]any)
	for _, v := range blocks {
		b, _ := v.(map[string]any)
		if transcript.String(b, "text") == "" && strings.Contains(transcript.String(b, "type"), "image") {
			out = transcript.AppendAttachmentReference(out, b)
		}
	}
	if _, images, ok := codexTypedToolOutput(payload["output"]); ok && images > 0 {
		outputs, _ := payload["output"].([]any)
		for _, v := range outputs {
			if b, _ := v.(map[string]any); transcript.String(b, "type") == "input_image" {
				out = transcript.AppendAttachmentReference(out, b)
			}
		}
	}
	return out
}
