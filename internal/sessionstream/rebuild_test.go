package sessionstream

import (
	"errors"
	"fmt"
	"testing"
)

func TestARebuildGrowsARowAndAsksToResyncWhenAnAppendDoesNotContinueIt(t *testing.T) {
	rebuild := NewRebuild()
	if _, err := rebuild.Apply(4, []byte(`{"seq":4,"type":"append","itemId":"r","from":0,"text":"x"}`)); !errors.Is(err, ErrResync) {
		t.Fatalf("an append for a row never seen: err=%v, want a resync", err)
	}
	if _, err := rebuild.Apply(5, []byte(`{"seq":5,"type":"item","data":{"itemId":"r","kind":"text","role":"assistant","body":"hé","truncated":true,"version":1}}`)); err != nil {
		t.Fatal(err)
	}
	row, err := rebuild.Apply(6, []byte(`{"seq":6,"type":"append","itemId":"r","from":3,"text":"llo","version":2,"final":true}`))
	if err != nil || row.Body != "héllo" || row.Seq != 6 || row.Version != 2 || !row.Final || row.Truncated || row.Kind != "text" {
		t.Fatalf("grown row=%+v err=%v", row, err)
	}
	if _, err := rebuild.Apply(7, []byte(`{"seq":7,"type":"append","itemId":"r","from":2,"text":"!"}`)); !errors.Is(err, ErrResync) {
		t.Fatalf("an append from the wrong offset: err=%v, want a resync", err)
	}
}

func TestARebuildLetsGoOfItsLeastRecentRowAndStillGrowsALiveOne(t *testing.T) {
	rebuild := NewRebuild()
	whole := func(seq uint64, id string) {
		t.Helper()
		if _, err := rebuild.Apply(seq, []byte(fmt.Sprintf(`{"seq":%d,"type":"item","data":{"itemId":%q,"kind":"text","role":"assistant","body":"a","version":1}}`, seq, id))); err != nil {
			t.Fatal(err)
		}
	}
	whole(1, "old")
	whole(2, "live")
	body := 1
	for index := 0; index < 2*RebuildRowLimit; index++ {
		seq := uint64(3 + 2*index)
		whole(seq, fmt.Sprintf("row-%d", index))
		if _, err := rebuild.Apply(seq+1, []byte(fmt.Sprintf(`{"seq":%d,"type":"append","itemId":"live","from":%d,"text":"b"}`, seq+1, body))); err != nil {
			t.Fatalf("a live row after %d others: %v", index, err)
		}
		body++
		if len(rebuild.rows) > RebuildRowLimit {
			t.Fatalf("the rebuild holds %d rows, past its limit of %d", len(rebuild.rows), RebuildRowLimit)
		}
	}
	if _, err := rebuild.Apply(1<<20, []byte(`{"seq":1048576,"type":"append","itemId":"old","from":1,"text":"b"}`)); !errors.Is(err, ErrResync) {
		t.Fatalf("an append to a row let go: err=%v, want a resync", err)
	}
}
