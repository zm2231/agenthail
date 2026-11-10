package daemon

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestPinnedFamiliesSortBelowNeedsYouAndAboveActiveOldestPinFirst(t *testing.T) {
	f := newOrderFixture()
	f.rows["s16"].PinnedAt = f.now.Add(-time.Hour)
	f.rows["s13"].PinnedAt = f.now.Add(-2 * time.Hour)
	f.rows["s17"].PinnedAt = f.now.Add(-3 * time.Hour)
	f.rows["s17"].Attention = &SessionAttention{Kind: AttentionAsk}
	f.rows["s15"].Attention = &SessionAttention{Kind: AttentionAsk}

	for _, scope := range []string{"all", "recent", "current"} {
		order := f.order(t, scope)
		if len(order.Families) < 4 {
			t.Fatalf("%s: families %v", scope, order.Families)
		}
		needsYou := order.Families[:2]
		slices.Sort(needsYou)
		if !reflect.DeepEqual(needsYou, []string{"s15", "s17"}) {
			t.Fatalf("%s: first two families are %v, want the two that need you, the pinned one among them", scope, order.Families[:2])
		}
		if got := order.Families[2:4]; !reflect.DeepEqual(got, []string{"s13", "s16"}) {
			t.Fatalf("%s: families after needs-you are %v, want the pinned ones oldest pin first", scope, got)
		}
		alpha := order.Collapsed["p-alpha"]
		if !slices.Contains(alpha, "s13") || !slices.Contains(alpha, "s16") {
			t.Fatalf("%s: a collapsed project hides a pinned family: %v", scope, alpha)
		}
	}
}

func TestACursorPagesThroughThePinnedBand(t *testing.T) {
	a := sessionViewKey{Band: sessionBandPinned, At: time.Unix(10, 0), ID: "b"}
	b := sessionViewKey{Band: sessionBandPinned, At: time.Unix(20, 0), ID: "a"}
	if !a.before(b) || b.before(a) {
		t.Fatal("pins do not order oldest first")
	}
	if !(sessionViewKey{Band: sessionBandNeedsYou, At: time.Unix(30, 0)}).before(a) || !b.before(sessionViewKey{Band: sessionBandActive, At: time.Unix(40, 0)}) {
		t.Fatal("the pinned band is not between needs-you and active")
	}
}
