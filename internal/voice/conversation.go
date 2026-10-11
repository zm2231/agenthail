package voice

// The sequence is shared by all client-owned delegation result frames.
type conversation struct {
	contextSequence    uint64
	transcriptSequence uint64
}

func (c *conversation) reset() { *c = conversation{} }
