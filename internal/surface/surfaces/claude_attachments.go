package surfaces

import (
	"context"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) ReadAttachment(ctx context.Context, s *surface.Session, id string) (*surface.Attachment, []byte, error) {
	if _, err := c.sessionTranscript(s); err != nil {
		return nil, nil, err
	}
	return readTranscriptAttachment(ctx, s, id, claudeTranscript{}, c.transcriptPath)
}

func claudeAttachmentReferences(record map[string]any) []attachmentReference {
	message, _ := record["message"].(map[string]any)
	blocks, _ := message["content"].([]any)
	var out []attachmentReference
	for _, raw := range blocks {
		b, _ := raw.(map[string]any)
		switch str(b, "type") {
		case "image":
			out = appendAttachmentReference(out, b["source"])
		case "tool_result":
			for _, image := range toolResultImageContent(b["content"]) {
				out = appendAttachmentReference(out, image)
			}
		}
	}
	return out
}
