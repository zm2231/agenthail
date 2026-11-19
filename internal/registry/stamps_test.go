package registry

import "testing"

func TestAStampBeingWrittenIsStillSeenUntilItIsWritten(t *testing.T) {
	writing, release := make(chan struct{}), make(chan struct{})
	written := map[string]int{}
	q := newStampQueue(func(key string, value int) error {
		close(writing)
		<-release
		written[key] = value
		return nil
	})
	q.stamp("credential", 7)
	<-writing
	if used, ok := q.pendingStamp("credential"); !ok || used != 7 {
		t.Fatalf("during the write the stamp read as %d, %v; a read then would see neither it nor the stored value", used, ok)
	}
	close(release)
	q.wait()
	if _, ok := q.pendingStamp("credential"); ok {
		t.Fatal("a written stamp is still pending")
	}
	if written["credential"] != 7 {
		t.Fatalf("written=%v", written)
	}
}
