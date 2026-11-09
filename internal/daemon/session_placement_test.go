package daemon

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type placementMutation struct {
	field  string
	mutate func(*dashboardSession, time.Time)
}

var placementInputMutations = []placementMutation{
	{"Surface", func(r *dashboardSession, _ time.Time) { r.Surface = surface.KindCodex }},
	{"Name", func(r *dashboardSession, _ time.Time) { r.Name = "renamed" }},
	{"Cwd", func(r *dashboardSession, _ time.Time) { r.Cwd = "/elsewhere" }},
	{"Alias", func(r *dashboardSession, _ time.Time) { r.Alias = "nick" }},
	{"Status", func(r *dashboardSession, _ time.Time) { r.Status = surface.StatusUnknown }},
	{"WaitingOn", func(r *dashboardSession, now time.Time) {
		r.WaitingOn = &surface.Waiting{Kind: surface.WaitingQuestion, ID: "q", Since: now.Add(-time.Minute)}
	}},
	{"BackgroundTasks", func(r *dashboardSession, _ time.Time) { r.BackgroundTasks = &surface.BackgroundTasks{Running: true} }},
	{"UsageLimit", func(r *dashboardSession, now time.Time) {
		r.UsageLimit = &surface.UsageLimit{ResetsAt: now.Add(time.Hour)}
	}},
	{"TurnStop", func(r *dashboardSession, _ time.Time) {
		r.TurnStop = &surface.TurnStop{TurnID: "u1", Reason: "Claude API error: overloaded"}
	}},
	{"LimitResume", func(r *dashboardSession, now time.Time) {
		r.LimitResume = &dashboardLimitResume{State: registry.LimitResumeWaiting, RefusedAt: now}
	}},
	{"LastActive", func(r *dashboardSession, now time.Time) { r.LastActive = now.Add(-30 * time.Minute) }},
	{"QueueCount", func(r *dashboardSession, _ time.Time) { r.QueueCount = 2 }},
	{"Current", func(r *dashboardSession, _ time.Time) { r.Current = !r.Current }},
	{"CurrentReason", func(r *dashboardSession, _ time.Time) { r.Current, r.CurrentReason = true, "queued" }},
	{"Archived", func(r *dashboardSession, _ time.Time) { r.Archived = true }},
	{"KeepAlive", func(r *dashboardSession, now time.Time) {
		r.KeepAlive = &dashboardKeepAlive{FailedAt: now, Error: "refused"}
	}},
	{"Project", func(r *dashboardSession, _ time.Time) {
		r.Project = &CatalogProject{ID: "p-delta", DisplayName: "delta"}
	}},
	{"Checkout", func(r *dashboardSession, _ time.Time) {
		r.Checkout = &catalogCheckout{ID: "c-2", Path: "/w", Branch: "topic"}
	}},
	{"Subagent", func(r *dashboardSession, _ time.Time) { r.Subagent = &surface.Subagent{ParentID: "s01", RootID: "s01"} }},
	{"Subagent", func(r *dashboardSession, _ time.Time) {
		if r.Subagent != nil {
			moved := *r.Subagent
			moved.Depth, moved.Nickname = moved.Depth+1, "scout"
			r.Subagent = &moved
		}
	}},
	{"Source", func(r *dashboardSession, _ time.Time) { r.Source = "subAgent" }},
	{"Delegated", func(r *dashboardSession, _ time.Time) { r.Delegated = true }},
	{"ObservedSubagents", func(r *dashboardSession, _ time.Time) {
		r.ObservedSubagents = &surface.SubagentRollup{Count: 1, Working: 1}
	}},
	{"PinnedAt", func(r *dashboardSession, now time.Time) { r.PinnedAt = now }},
	{"Snooze", func(r *dashboardSession, now time.Time) {
		r.Snooze = &dashboardSnooze{SnoozedAt: now, Until: now.Add(time.Hour), Wake: registry.SnoozeWakeTime}
	}},
	{"Host", func(r *dashboardSession, _ time.Time) {
		r.Host = &dashboardHost{ID: "h1", Name: "vps", State: hostStateUnreachable}
	}},
}

// Attention and ReplacedLead reach clients as delivery.* and coordinator.* events; RegisteredAt never changes after insert.
var placementExempt = map[string]bool{"RegisteredAt": true, "Attention": true, "ReplacedLead": true, "Display": true, "InboxSection": true, "ID": true, "Placement": true}

var placementIgnoredMutations = map[string]func(*dashboardSession, time.Time){
	"RuntimeKind": func(r *dashboardSession, _ time.Time) { r.RuntimeKind = surface.RuntimeClaudeCLI },
	"Settings": func(r *dashboardSession, _ time.Time) {
		r.Settings = &surface.ObservedSettings{Model: "other", Effort: "xhigh", Permission: "plan"}
	},
	"ClaudeConfigRoot": func(r *dashboardSession, _ time.Time) {
		r.ClaudeConfigRoot = &surface.SessionConfigRoot{Name: "work", Root: "/w/.claude"}
	},
	"Activity": func(r *dashboardSession, now time.Time) {
		r.Activity = &surface.Activity{BusySince: now.Add(-time.Hour), LastProgressAt: now.Add(-20 * time.Minute), NoProcess: true}
	},
	"Plan":              func(r *dashboardSession, _ time.Time) { r.Plan = &surface.Plan{Done: 1, Total: 3} },
	"Open":              func(r *dashboardSession, _ time.Time) { r.Open = !r.Open },
	"Ended":             func(r *dashboardSession, _ time.Time) { r.Ended = !r.Ended },
	"Capabilities":      func(r *dashboardSession, _ time.Time) { r.Capabilities.Send = !r.Capabilities.Send },
	"ReadOnly":          func(r *dashboardSession, _ time.Time) { r.ReadOnly = !r.ReadOnly },
	"ReadOnlyReason":    func(r *dashboardSession, _ time.Time) { r.ReadOnlyReason = "elsewhere" },
	"Transport":         func(r *dashboardSession, _ time.Time) { r.Transport = "desktop" },
	"ForkedFrom":        func(r *dashboardSession, _ time.Time) { r.ForkedFrom = "s05" },
	"StartedBy":         func(r *dashboardSession, _ time.Time) { r.StartedBy = "s05" },
	"Task":              func(r *dashboardSession, _ time.Time) { r.Task = true },
	"ObservedAt":        func(r *dashboardSession, now time.Time) { r.ObservedAt = now },
	"UnavailableReason": func(r *dashboardSession, _ time.Time) { r.UnavailableReason = "gone" },
	"Worktree": func(r *dashboardSession, _ time.Time) {
		r.Worktree = &workView{Path: "/w/.worktrees/ah-x", State: "active"}
	},
	"PRs": func(r *dashboardSession, _ time.Time) {
		r.PRs = []prView{{Repo: "github.com/acme/app", Number: 1}}
	},
	"Runtime": func(r *dashboardSession, _ time.Time) {
		r.Runtime = &surface.Runtime{Launcher: surface.LauncherExternal, Focusable: true}
	},
	"Freshness": func(r *dashboardSession, _ time.Time) { r.Freshness = &registry.CatalogFreshness{Stale: true} },
	"Subagents": func(r *dashboardSession, _ time.Time) { r.Subagents = &surface.SubagentRollup{Count: 4, Working: 2} },
	"Holders":   func(r *dashboardSession, _ time.Time) { r.Holders = []surface.Holder{{}} },
	"HiddenSubagents": func(r *dashboardSession, _ time.Time) {
		r.HiddenSubagents = &surface.SubagentRollup{Count: 3, Working: 1}
	},
	"LiveHiddenSubagents": func(r *dashboardSession, _ time.Time) {
		r.LiveHiddenSubagents = &surface.SubagentRollup{Count: 2, Working: 1}
	},
	"Listed": func(r *dashboardSession, _ time.Time) { r.Listed = !r.Listed },
	"EndTurn": func(r *dashboardSession, now time.Time) {
		r.EndTurn = &dashboardEndTurn{Headline: "Deployed", Needs: endTurnNeedsNothing, EndedAt: now}
	},
	"TurnCompletion": func(r *dashboardSession, now time.Time) {
		r.TurnCompletion = &dashboardTurnCompletion{TurnID: "t9", Outcome: turnOutcomeFailed, At: now}
	},
}

func TestSessionPlacementClassifiesEveryRowField(t *testing.T) {
	inputs := map[string]bool{}
	for _, mutation := range placementInputMutations {
		inputs[mutation.field] = true
	}
	fields := reflect.TypeFor[dashboardSession]()
	for i := range fields.NumField() {
		name := fields.Field(i).Name
		_, ignored := placementIgnoredMutations[name]
		classes := 0
		for _, in := range []bool{inputs[name], ignored, placementExempt[name]} {
			if in {
				classes++
			}
		}
		if classes != 1 {
			t.Errorf("dashboardSession.%s must be exactly one of: a placement input, an ignored field with a mutation, or exempt", name)
		}
	}
}

func viewSignature(t *testing.T, f *orderFixture) string {
	t.Helper()
	rows := make([]dashboardSession, 0, len(f.ids))
	for _, id := range f.ids {
		row := *f.rows[id]
		display := sessionDisplay(row, f.now)
		row.Display = &display
		row.Attention = rowAttention(row, 0, "")
		rows = append(rows, row)
	}
	filters := []SessionViewFilters{{}, {Surface: string(surface.KindClaude)}, {Surface: string(surface.KindCodex)}, {Query: "renamed"}, {Query: "nick"}, {Query: "elsewhere"}, {Query: "topic"}, {Query: "vps"}}
	for _, kind := range []string{"unavailable", "waiting", "working", "limited", "finishing", "queued", "offline", "idle"} {
		filters = append(filters, SessionViewFilters{Status: kind})
	}
	var views []any
	for _, scope := range []string{"running", "current", "recent", "all", "archived"} {
		for _, filter := range filters {
			plan := newSessionViewPlan(rows, scope, filter, f.now)
			views = append(views, plan.view.Counts, plan.view.Projects, plan.view.Families)
		}
	}
	encoded, err := json.Marshal(views)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestSessionPlacementChangesWithEveryOrderInput(t *testing.T) {
	for _, mutation := range placementInputMutations {
		used := false
		for _, target := range []string{"s02", "s00", "child"} {
			f := newOrderFixture()
			beforeView, before := viewSignature(t, f), sessionPlacement(*f.rows[target])
			mutation.mutate(f.rows[target], f.now)
			afterView, after := viewSignature(t, f), sessionPlacement(*f.rows[target])
			if afterView != beforeView && after == before {
				t.Errorf("%s on %s changed the view but not the placement", mutation.field, target)
			}
			if target == "s00" && mutation.field == "LastActive" && (after != before || afterView != beforeView) {
				t.Errorf("a busy row's lastActive must move neither the view nor its placement")
			}
			used = used || after != before
		}
		if !used {
			t.Errorf("%s never changed the placement", mutation.field)
		}
	}
}

func TestSessionViewIgnoresFieldsPlacementLeavesOut(t *testing.T) {
	for _, target := range []string{"s02", "s00", "child"} {
		for field, mutate := range placementIgnoredMutations {
			f := newOrderFixture()
			before, placed := viewSignature(t, f), sessionPlacement(*f.rows[target])
			mutate(f.rows[target], f.now)
			if viewSignature(t, f) != before {
				t.Errorf("%s on %s changed the view, so it must be a placement input", field, target)
			}
			if sessionPlacement(*f.rows[target]) != placed {
				t.Errorf("%s on %s changed the placement of a field the view ignores", field, target)
			}
		}
	}
}

func TestSessionPlacementIgnoresProgressOfABusyRow(t *testing.T) {
	f := newOrderFixture()
	row := f.rows["s00"]
	before := sessionPlacement(*row)
	row.LastActive = row.LastActive.Add(3 * time.Second)
	row.Activity = &surface.Activity{BusySince: row.Activity.BusySince, LastProgressAt: row.LastActive, Tool: &surface.RunningTool{Name: "Bash", StartedAt: row.LastActive}}
	row.Plan = &surface.Plan{Done: 2, Total: 5}
	row.ObservedAt = row.LastActive
	if sessionPlacement(*row) != before {
		t.Fatal("a progress write on a busy row changed its placement")
	}
}

func TestSessionPlacementOnTheStreamMatchesTheRow(t *testing.T) {
	f := newOrderFixture()
	for _, id := range []string{"s00", "s02", "child", "loose"} {
		row := *f.rows[id]
		payload, err := json.Marshal(map[string]any{"session": row})
		if err != nil {
			t.Fatal(err)
		}
		served, err := withPlacement(payload)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Session map[string]any `json:"session"`
		}
		if err := json.Unmarshal(served, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Session["placement"] != sessionPlacement(row) {
			t.Errorf("%s: stream placement %v, row placement %s", id, decoded.Session["placement"], sessionPlacement(row))
		}
		if decoded.Session["id"] != id || decoded.Session["lastActive"] == nil {
			t.Errorf("%s: the stream row lost its fields: %v", id, decoded.Session)
		}
	}
}
