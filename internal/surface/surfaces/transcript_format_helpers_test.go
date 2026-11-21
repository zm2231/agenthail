package surfaces

func transcriptFormatOf(source string) transcriptFormat {
	if source == "claude" {
		return claudeTranscript{}
	}
	return codexTranscript{}
}
