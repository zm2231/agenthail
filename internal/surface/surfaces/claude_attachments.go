package surfaces

import (
	"context"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func (c *Claude) ReadAttachment(ctx context.Context, s *surface.Session, id string) (*surface.Attachment, []byte, error) {
	if _, err := c.sessionTranscript(s); err != nil {
		return nil, nil, err
	}
	return transcript.ReadAttachment(ctx, s, id, claudeTranscript{}, c.transcriptPath)
}

func claudeAttachmentReferences(record map[string]any) []transcript.AttachmentReference {
	message, _ := record["message"].(map[string]any)
	blocks, _ := message["content"].([]any)
	var out []transcript.AttachmentReference
	for _, raw := range blocks {
		b, _ := raw.(map[string]any)
		switch transcript.String(b, "type") {
		case "image":
			out = transcript.AppendAttachmentReference(out, b["source"])
		case "tool_result":
			for _, image := range transcript.ToolResultImageContent(b["content"]) {
				out = transcript.AppendAttachmentReference(out, image)
			}
		}
	}
	return out
}
