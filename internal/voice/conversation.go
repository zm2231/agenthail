package voice

import (
	"sort"
	"time"
)

// conversation owns quiet worker context independently of the provider's audio.
// Its caller holds Service.mu. Response completion is not playback completion;
// the quiet interval is a scheduling guard, not evidence of speaker completion.
type conversation struct {
	responses       map[string]bool
	canonicalSpeech bool
	pending         map[string]map[string]any
	emitted         map[string]string
	lastActivity    time.Time
	flushing        uint64
	flushActive     bool
	contextSequence uint64
}

func (c *conversation) reset() { *c = conversation{} }

func (c *conversation) observe(e Event, now time.Time) {
	item, _ := e.Params["item"].(map[string]any)
	if item["type"] != "transcriptSegment" {
		role, _ := e.Params["role"].(string)
		if role != "assistant" || c.canonicalSpeech {
			return
		}
		if c.responses == nil {
			c.responses = map[string]bool{}
		}
		switch e.Method {
		case "thread/realtime/transcript/delta":
			c.responses["transcript:"+role] = true
		case "thread/realtime/transcript/done":
			delete(c.responses, "transcript:"+role)
		default:
			return
		}
		c.lastActivity = now
		return
	}
	// Input segments can span multiple delegated turns. They are transcript
	// assembly, not ownership of output audio.
	if item["role"] != "assistant" {
		return
	}
	id, _ := item["id"].(string)
	if id == "" {
		return
	}
	if c.responses == nil {
		c.responses = map[string]bool{}
	}
	c.canonicalSpeech = true
	delete(c.responses, "transcript:assistant")
	switch e.Method {
	case "thread/realtime/item/started":
		c.responses[id] = true
	case "thread/realtime/item/completed":
		delete(c.responses, id)
	default:
		return
	}
	c.lastActivity = now
}

func (c *conversation) stage(data map[string]any) {
	item, _ := data["item"].(map[string]any)
	id, _ := item["itemId"].(string)
	if id == "" {
		return
	}
	if c.pending == nil {
		c.pending = map[string]map[string]any{}
	}
	// Superseding snapshots remain whole: a delayed flush must never depend on
	// another delta having reached the provider first.
	c.pending[id] = data
	if len(c.pending) > 64 {
		delete(c.pending, c.ordered()[0])
	}
}

func (c *conversation) ordered() []string {
	ids := make([]string, 0, len(c.pending))
	for id := range c.pending {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, _ := c.pending[ids[i]]["sequence"].(uint64)
		b, _ := c.pending[ids[j]]["sequence"].(uint64)
		if a == b {
			return ids[i] < ids[j]
		}
		return a < b
	})
	return ids
}

func (c *conversation) ready(now time.Time) bool {
	return len(c.pending) > 0 && len(c.responses) == 0 && now.Sub(c.lastActivity) >= 750*time.Millisecond
}
