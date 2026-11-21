package surfaces

import "github.com/zm2231/agenthail/internal/surface/transcript"

func transcriptFormatOf(source string) transcript.Format {
	if source == "claude" {
		return claudeTranscript{}
	}
	return codexTranscript{}
}
