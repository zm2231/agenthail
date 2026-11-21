package daemon

import "testing"

func TestAWriteWithoutAClientRequestIDActsEachTime(t *testing.T) {
	f := newMCPWriteFixture(t)
	for range 2 {
		if out, refused := callMCP(t, f.parent, "agenthail_send", map[string]any{"target": "to", "message": "first contact"}); refused {
			t.Fatalf("send without clientRequestId refused: %v", out)
		}
	}
	if delivered := len(f.fake.sent) + len(f.fake.steered) + f.r.QueueCount("to"); delivered != 2 {
		t.Fatalf("sent=%v steered=%v queued=%d, want each call delivered once", f.fake.sent, f.fake.steered, f.r.QueueCount("to"))
	}
}
