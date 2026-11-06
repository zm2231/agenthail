package sessionstream

import (
	"errors"
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
