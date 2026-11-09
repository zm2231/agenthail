package registry

import "testing"

func TestAHeldHostActionIsHeldOncePerKeyAndReplayedUntilSettled(t *testing.T) {
	r := openTestRegistry(t)
	first, err := r.HoldHostAction("h1", "send-1:h1", "#p", []byte(`{"action":"channel-deliver"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.HoldHostAction("h1", "send-1:h1", "#p", []byte(`{"action":"channel-deliver"}`), true)
	if err != nil || again != first {
		t.Fatalf("a key already held returns its row: %d vs %d, %v", again, first, err)
	}
	if _, err := r.HoldHostAction("h2", "send-1:h2", "#p", []byte(`{}`), true); err != nil {
		t.Fatal(err)
	}
	held, err := r.HostHeldActions("h1")
	if err != nil || len(held) != 1 || held[0].Status != HostActionPending || held[0].Key != "send-1:h1" {
		t.Fatalf("h1 holds its one pending action: %+v %v", held, err)
	}
	if _, claimed, err := r.ClaimHostHeldAction(first); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if err := r.ReleaseHostHeldAction(first); err != nil {
		t.Fatal(err)
	}
	if held, _ := r.HostHeldActions("h1"); len(held) != 1 || held[0].Status != HostActionPending {
		t.Fatalf("a released action is pending again: %+v", held)
	}
	if err := r.SettleHostHeldAction(first, HostActionDone, `{"sent":1}`); err != nil {
		t.Fatal(err)
	}
	if held, _ := r.HostHeldActions("h1"); len(held) != 0 {
		t.Fatalf("a settled action is no longer held: %+v", held)
	}
	if _, claimed, _ := r.ClaimHostHeldAction(first); claimed {
		t.Fatal("a settled action is never claimed again")
	}
	if held, _ := r.HostHeldActions("h2"); len(held) != 1 || held[0].Status != HostActionInflight {
		t.Fatalf("an action that may have reached its host is held inflight: %+v", held)
	}
	if err := r.SettleHostHeldAction(first, "maybe", ""); err == nil {
		t.Fatal("only done or dead settle an action")
	}
}

func TestRemovingAHostDropsTheActionsHeldForIt(t *testing.T) {
	r := openTestRegistry(t)
	const host = "h0123456789ae"
	if _, err := r.AddHost(PairedHost{ID: host, Name: "vps", Endpoint: "https://vps.example.ts.net", Address: "100.64.0.2", Token: "ahd_synthetic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.HoldHostAction(host, "send-1:"+host, "#p", []byte(`{}`), false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RemoveHost(host); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM host_held_actions WHERE host_id=?`, host).Scan(&left); err != nil || left != 0 {
		t.Fatalf("left=%d err=%v, want an unpaired host's held actions gone", left, err)
	}
}
