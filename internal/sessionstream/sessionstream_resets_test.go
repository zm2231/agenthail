package sessionstream

import "testing"

func TestADecodedNoticeKeepsItsResetTime(t *testing.T) {
	event, err := DecodePayload(7, []byte(`{"itemId":"n","kind":"notice","status":"usage_limit","resetsAt":"2026-10-21T11:00:00Z","body":"limit"}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.ResetsAt != "2026-10-21T11:00:00Z" || event.Status != "usage_limit" {
		t.Fatalf("event=%+v", event)
	}
}
