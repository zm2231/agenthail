package surfaces

import (
	"context"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) ReadAttachment(ctx context.Context, s *surface.Session, id string) (*surface.Attachment, []byte, error) {
	return readTranscriptAttachment(ctx, s, id, codexTranscript{}, c.home.transcriptPath)
}

func codexAttachmentReferences(record map[string]any) []attachmentReference {
	payload, _ := record["payload"].(map[string]any)
	var out []attachmentReference
	if images, ok := payload["images"].([]any); ok {
		for _, v := range images {
			out = appendAttachmentReference(out, v)
		}
	}
	blocks, _ := payload["content"].([]any)
	for _, v := range blocks {
		b, _ := v.(map[string]any)
		if str(b, "text") == "" && strings.Contains(str(b, "type"), "image") {
			out = appendAttachmentReference(out, b)
		}
	}
	if _, images, ok := codexTypedToolOutput(payload["output"]); ok && images > 0 {
		outputs, _ := payload["output"].([]any)
		for _, v := range outputs {
			if b, _ := v.(map[string]any); str(b, "type") == "input_image" {
				out = appendAttachmentReference(out, b)
			}
		}
	}
	return out
}
