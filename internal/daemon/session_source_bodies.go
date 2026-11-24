package daemon

import "container/list"

// sessionSourceBodyBytes bounds the item text a source keeps to extend its
// next append. A body it no longer keeps is read back from the journal row
// when the item is appended to again, so the bound only trades memory for that
// read; the most recently written body is always kept.
const sessionSourceBodyBytes = sessionJournalRetentionBytes / 4

type sourceBodies struct {
	bytes   int
	order   list.List
	entries map[string]*list.Element
}

type sourceBody struct {
	key  string
	body *streamedBody
}

func (b *sourceBodies) get(key string) *streamedBody {
	element := b.entries[key]
	if element == nil {
		return nil
	}
	b.order.MoveToFront(element)
	return element.Value.(*sourceBody).body
}

func (b *sourceBodies) put(key string, body *streamedBody) {
	if b.entries == nil {
		b.entries = map[string]*list.Element{}
	}
	if element := b.entries[key]; element != nil {
		b.bytes -= len(element.Value.(*sourceBody).body.text)
		element.Value.(*sourceBody).body = body
		b.order.MoveToFront(element)
	} else {
		b.entries[key] = b.order.PushFront(&sourceBody{key: key, body: body})
	}
	b.bytes += len(body.text)
	for b.bytes > sessionSourceBodyBytes && b.order.Len() > 1 {
		oldest := b.order.Remove(b.order.Back()).(*sourceBody)
		delete(b.entries, oldest.key)
		b.bytes -= len(oldest.body.text)
	}
}
