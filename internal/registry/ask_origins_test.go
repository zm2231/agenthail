package registry

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func declaredAskOrigins(t *testing.T) map[string]string {
	t.Helper()
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	origins := map[string]string{}
	fset := token.NewFileSet()
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			spec, ok := node.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range spec.Names {
				if !strings.HasPrefix(name.Name, "AskOrigin") || strings.HasPrefix(name.Name, "AskOrigins") {
					continue
				}
				if i >= len(spec.Values) {
					t.Fatalf("%s in %s has no value of its own; declare ask origins as literals so this check can read them", name.Name, file.Name())
				}
				literal, ok := spec.Values[i].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					t.Fatalf("%s in %s is not a plain string literal; declare ask origins as literals so this check can read them", name.Name, file.Name())
				}
				value, _ := strconv.Unquote(literal.Value)
				origins[name.Name] = value
			}
			return true
		})
	}
	return origins
}

func TestEveryAskOriginIsEitherShownOnRowsOrHiddenWithAReason(t *testing.T) {
	origins := declaredAskOrigins(t)
	if len(origins) < 7 {
		t.Fatalf("found only %v; the scan of AskOrigin constants is broken", origins)
	}
	for name, origin := range origins {
		shown := slices.Contains(AskOriginsOnRows, origin)
		reason, hidden := AskOriginsOffRows[origin]
		switch {
		case shown && hidden:
			t.Errorf("%s (%q) is both shown on rows and hidden from them", name, origin)
		case !shown && !hidden:
			t.Errorf("%s (%q) is new: add it to AskOriginsOnRows so the person sees it, or to AskOriginsOffRows with the reason it stays hidden", name, origin)
		case hidden && strings.TrimSpace(reason) == "":
			t.Errorf("%s (%q) is hidden from rows without a reason", name, origin)
		}
	}
	declared := map[string]bool{}
	for _, origin := range origins {
		declared[origin] = true
	}
	for _, origin := range AskOriginsOnRows {
		if !declared[origin] {
			t.Errorf("AskOriginsOnRows lists %q, which no AskOrigin constant declares", origin)
		}
	}
	for origin := range AskOriginsOffRows {
		if !declared[origin] {
			t.Errorf("AskOriginsOffRows lists %q, which no AskOrigin constant declares", origin)
		}
	}
}

func TestEveryOriginShownOnRowsReachesTheRow(t *testing.T) {
	r := coordinatorRegistry(t, "asker")
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	for i, origin := range AskOriginsOnRows {
		ask, err := r.RaiseAsk(Ask{SessionID: "asker", Origin: origin, Title: origin, RoutedTo: AskToPerson, Questions: []AskQuestion{{Question: origin}}}, at.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		rows, err := r.RowAsks()
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(rows["asker"].Open, ask.ID) {
			t.Fatalf("an open %s ask for the person is missing from the row: %+v", origin, rows["asker"])
		}
		if claimed, err := r.ClaimAsk(ask.ID, AskAnswer{Note: "done"}, AskToPerson, "test", at.Add(time.Hour)); err != nil || !claimed {
			t.Fatalf("answer the %s ask: %v %v", origin, claimed, err)
		}
	}
	for i, origin := range slices.Sorted(func(yield func(string) bool) {
		for origin := range AskOriginsOffRows {
			if !yield(origin) {
				return
			}
		}
	}) {
		ask, err := r.RaiseAsk(Ask{SessionID: "asker", Origin: origin, Title: origin, RoutedTo: AskToPerson, Questions: []AskQuestion{{Question: origin}}}, at.Add(2*time.Hour+time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		rows, err := r.RowAsks()
		if err != nil {
			t.Fatal(err)
		}
		if row := rows["asker"]; row.ID == ask.ID || slices.Contains(row.Open, ask.ID) {
			t.Fatalf("a %s ask, hidden from rows, reached the row: %+v", origin, row)
		}
	}
}

func TestARetiredSessionWithAnOpenAskForThePersonStaysCurrent(t *testing.T) {
	r := openTestRegistry(t)
	ids := []string{"lead-routed", "hidden"}
	for _, origin := range AskOriginsOnRows {
		ids = append(ids, "asks-"+origin)
	}
	for _, id := range ids {
		recordEndedRow(t, r, surface.Session{ID: id}, `{"id":"`+id+`","current":false}`)
	}
	endedPasses(t, r, 2)
	raise := func(session, origin, to string) {
		t.Helper()
		if _, err := r.RaiseAsk(Ask{SessionID: session, Origin: origin, Title: origin, RoutedTo: to, Questions: []AskQuestion{{Question: origin}}}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for _, origin := range AskOriginsOnRows {
		raise("asks-"+origin, origin, AskToPerson)
	}
	raise("lead-routed", AskOriginAgent, "lead")
	raise("hidden", AskOriginExtensionChange, AskToPerson)
	read, err := r.CatalogPresenceRows(nil, "", 0, func(string, int64) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, row := range read.Rows {
		found[row.Session.ID] = true
	}
	for _, origin := range AskOriginsOnRows {
		if !found["asks-"+origin] {
			t.Errorf("a retired session with an open %s ask for the person left the current reads", origin)
		}
	}
	if found["lead-routed"] || found["hidden"] {
		t.Errorf("an ask the person does not see brought a retired session back: %v", found)
	}
	for _, id := range ids {
		if !isRetired(t, r, id) {
			t.Errorf("%s was unretired rather than read while its ask is open", id)
		}
	}
}

func TestAskWaitsOnPersonFollowsTheRowOrigins(t *testing.T) {
	for _, origin := range AskOriginsOnRows {
		if !AskWaitsOnPerson(Ask{Origin: origin, State: AskOpen, RoutedTo: AskToPerson}) {
			t.Errorf("an open %s ask for the person must count as waiting on them", origin)
		}
	}
	for origin := range AskOriginsOffRows {
		if AskWaitsOnPerson(Ask{Origin: origin, State: AskOpen, RoutedTo: AskToPerson}) {
			t.Errorf("a hidden %s ask must not count", origin)
		}
	}
	if AskWaitsOnPerson(Ask{Origin: AskOriginAgent, State: AskOpen, RoutedTo: "lead"}) || AskWaitsOnPerson(Ask{Origin: AskOriginAgent, State: AskAnswered, RoutedTo: AskToPerson}) {
		t.Error("an ask routed elsewhere or already answered must not count")
	}
	if !AskWaitsOnPerson(Ask{Origin: AskOriginDelivery, State: AskOpen, RoutedTo: "lead"}) {
		t.Error("an open delivery ask counts wherever it is routed, as before")
	}
}
