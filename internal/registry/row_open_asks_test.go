package registry

import (
	"slices"
	"testing"
	"time"
)

func TestTheRowListsEveryOpenAskForThePersonOldestFirst(t *testing.T) {
	r := coordinatorRegistry(t, "asker", "other")
	at := time.Date(2026, 11, 15, 22, 5, 0, 0, time.UTC)
	raise := func(session, title, to, origin string, when time.Time) Ask {
		t.Helper()
		ask, err := r.RaiseAsk(Ask{SessionID: session, Origin: origin, Title: title, RoutedTo: to, Questions: []AskQuestion{{Question: title}}}, when)
		if err != nil {
			t.Fatal(err)
		}
		return ask
	}
	ship := raise("asker", "Ship the resolver fix", AskToPerson, AskOriginAgent, at)
	login := raise("asker", "Sign in", AskToPerson, AskOriginAgent, at.Add(3*time.Hour))
	raise("asker", "For the lead", "lead", AskOriginAgent, at.Add(time.Hour))
	raise("other", "Elsewhere", AskToPerson, AskOriginAgent, at)

	rows, err := r.RowAsks()
	if err != nil {
		t.Fatal(err)
	}
	if row := rows["asker"]; row.ID != ship.ID || !slices.Equal(row.Open, []string{ship.ID, login.ID}) {
		t.Fatalf("the row names %s with open %v, want the oldest %s with both of the person's asks in order", row.ID, row.Open, ship.ID)
	}

	if claimed, err := r.ClaimAsk(login.ID, AskAnswer{Note: "done"}, "person", "test", at.Add(4*time.Hour)); err != nil || !claimed {
		t.Fatalf("answer the newer ask: %v %v", claimed, err)
	}
	rows, err = r.RowAsks()
	if err != nil {
		t.Fatal(err)
	}
	if row := rows["asker"]; row.ID != ship.ID || !slices.Equal(row.Open, []string{ship.ID}) {
		t.Fatalf("after the newer ask is answered the row has open %v, want only %s", row.Open, ship.ID)
	}

	if claimed, err := r.ClaimAsk(ship.ID, AskAnswer{Note: "ship"}, "person", "test", at.Add(5*time.Hour)); err != nil || !claimed {
		t.Fatalf("answer the older ask: %v %v", claimed, err)
	}
	rows, err = r.RowAsks()
	if err != nil {
		t.Fatal(err)
	}
	if row := rows["asker"]; row.RoutedTo == AskToPerson && row.State == AskOpen || row.Open != nil {
		t.Fatalf("with no open ask for the person the row %s lists open %v", row.ID, row.Open)
	}
}

func TestAnAskReroutedAwayLeavesTheNewerOneForThePersonOnTheRow(t *testing.T) {
	r := coordinatorRegistry(t, "asker")
	at := time.Date(2026, 11, 15, 22, 5, 0, 0, time.UTC)
	var ids []string
	for i, title := range []string{"Ship the resolver fix", "Sign in"} {
		ask, err := r.RaiseAsk(Ask{SessionID: "asker", Origin: AskOriginAgent, Title: title, RoutedTo: AskToPerson, Questions: []AskQuestion{{Question: title}}}, at.Add(time.Duration(i)*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, ask.ID)
	}
	if moved, err := r.RouteAsk(ids[0], "lead", "escalated", at.Add(2*time.Hour)); err != nil || !moved {
		t.Fatalf("route the older ask to the lead: %v %v", moved, err)
	}
	rows, err := r.RowAsks()
	if err != nil {
		t.Fatal(err)
	}
	if row := rows["asker"]; row.ID != ids[1] || row.RoutedTo != AskToPerson || !slices.Equal(row.Open, []string{ids[1]}) {
		t.Fatalf("the row names %s routed to %q with open %v, want the person's %s", row.ID, row.RoutedTo, row.Open, ids[1])
	}
}
