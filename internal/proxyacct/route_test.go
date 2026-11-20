package proxyacct_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
)

type reading struct {
	kind     string
	percent  float64
	ceiling  float64
	resets   time.Duration
	noReset  bool
	noReport bool
}

func TestRoutingNamesTheAccountTheProxySendsWorkTo(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	week := func(percent, ceiling float64, resets time.Duration) reading {
		return reading{kind: proxyacct.WindowWeek, percent: percent, ceiling: ceiling, resets: resets}
	}
	fable := func(percent, ceiling float64, resets time.Duration) reading {
		return reading{kind: proxyacct.WindowWeekModel, percent: percent, ceiling: ceiling, resets: resets}
	}
	claude := func(id, status string, preference float64, readings ...reading) proxyacct.Account {
		account := proxyacct.Account{ID: id, Provider: "claude", Label: id + "@example.com", Status: status, Preference: preference}
		for _, r := range readings {
			resets := now.Add(r.resets)
			if r.noReset {
				resets = time.Time{}
			}
			account.Windows = append(account.Windows, proxyacct.Window{Kind: r.kind, Percent: r.percent, Ceiling: r.ceiling, ResetsAt: resets})
			account.Drain = append(account.Drain, proxyacct.Drain{Kind: r.kind, Percent: r.percent, HasPercent: !r.noReport, ResetsAt: resets, Ceiling: r.ceiling})
		}
		if status == proxyacct.StatusCooling {
			account.NextRetry = now.Add(31 * time.Hour)
		}
		return account
	}
	codex := func(id string, preference float64, resets time.Duration) proxyacct.Account {
		return proxyacct.Account{ID: id, Provider: "codex", Label: id + "@example.com", Status: proxyacct.StatusActive, Preference: preference, Windows: []proxyacct.Window{{Kind: proxyacct.WindowWeek, Percent: 23, ResetsAt: now.Add(resets)}}}
	}
	for _, c := range []struct {
		name     string
		accounts []proxyacct.Account
		fable    bool
		want     proxyacct.Route
	}{
		{
			name: "a cooling account at 100% is passed over for the preferred one at 13%",
			accounts: []proxyacct.Account{
				claude("main", proxyacct.StatusActive, 10, week(13, 0, 25*time.Hour)),
				claude("team", proxyacct.StatusCooling, 10, week(100, 0, 30*time.Hour)),
				claude("spare", proxyacct.StatusActive, 0),
				codex("cx", 0, 40*time.Hour),
			},
			want: proxyacct.Route{Lead: "main", Routed: map[string]string{"claude": "main", "codex": "cx"}},
		},
		{
			name: "a preferred account at its ceiling gives way to one under its ceiling",
			accounts: []proxyacct.Account{
				claude("main", proxyacct.StatusActive, 10, week(61, 60, 25*time.Hour)),
				claude("spare", proxyacct.StatusActive, 0, week(20, 0, 90*time.Hour)),
				claude("off", proxyacct.StatusDisabled, 10),
			},
			want: proxyacct.Route{Lead: "spare", Routed: map[string]string{"claude": "spare"}},
		},
		{
			name: "among equal preferences the soonest weekly reset goes first",
			accounts: []proxyacct.Account{
				claude("late", proxyacct.StatusActive, 10, week(5, 0, 90*time.Hour)),
				claude("soon", proxyacct.StatusActive, 10, week(50, 0, 10*time.Hour)),
			},
			want: proxyacct.Route{Lead: "soon", Routed: map[string]string{"claude": "soon"}},
		},
		{
			name: "an account with no weekly reset known goes before ones with a reset of the same preference",
			accounts: []proxyacct.Account{
				claude("soon", proxyacct.StatusActive, 10, week(50, 0, 10*time.Hour)),
				claude("new", proxyacct.StatusActive, 10),
			},
			want: proxyacct.Route{Lead: "new", Routed: map[string]string{"claude": "new"}},
		},
		{
			name: "a reset with no percent still counts as a weekly reading",
			accounts: []proxyacct.Account{
				claude("late", proxyacct.StatusActive, 10, week(5, 0, 90*time.Hour)),
				claude("reset-only", proxyacct.StatusActive, 10, reading{kind: proxyacct.WindowWeek, resets: 10 * time.Hour, noReport: true}),
			},
			want: proxyacct.Route{Lead: "reset-only", Routed: map[string]string{"claude": "reset-only"}},
		},
		{
			name: "the per-model week's reset orders an account without a week reading",
			accounts: []proxyacct.Account{
				claude("late", proxyacct.StatusActive, 10, week(5, 0, 90*time.Hour)),
				claude("fable", proxyacct.StatusActive, 10, fable(5, 0, 10*time.Hour)),
			},
			want: proxyacct.Route{Lead: "fable", Routed: map[string]string{"claude": "fable"}},
		},
		{
			name: "a window past its reset counts as empty",
			accounts: []proxyacct.Account{
				claude("main", proxyacct.StatusActive, 10, week(99, 60, -time.Hour)),
				claude("spare", proxyacct.StatusActive, 0, week(10, 0, 90*time.Hour)),
			},
			want: proxyacct.Route{Lead: "main", Routed: map[string]string{"claude": "main"}},
		},
		{
			name: "a Fable-only ceiling binds only Fable work, so the account stays routed and the proxy is not full",
			accounts: []proxyacct.Account{
				claude("main", proxyacct.StatusActive, 10, week(30, 60, 25*time.Hour), fable(70, 60, 25*time.Hour)),
				claude("team", proxyacct.StatusCooling, 10),
			},
			want: proxyacct.Route{Lead: "main", Routed: map[string]string{"claude": "main"}},
		},
		{
			name: "for Fable work the per-model week's ceiling binds, so the account under it takes the work",
			accounts: []proxyacct.Account{
				claude("main", proxyacct.StatusActive, 10, week(30, 60, 25*time.Hour), fable(70, 60, 25*time.Hour)),
				claude("spare", proxyacct.StatusActive, 0, week(20, 60, 90*time.Hour), fable(10, 60, 90*time.Hour)),
			},
			fable: true,
			want:  proxyacct.Route{Lead: "spare", Routed: map[string]string{"claude": "spare"}},
		},
		{
			name: "for Fable work a proxy whose only room is Fable-capped is full",
			accounts: []proxyacct.Account{
				claude("main", proxyacct.StatusActive, 10, week(30, 60, 25*time.Hour), fable(70, 60, 20*time.Hour)),
				claude("team", proxyacct.StatusCooling, 10, week(100, 0, 30*time.Hour)),
			},
			fable: true,
			want:  proxyacct.Route{Lead: "team", Full: true, FreesAt: now.Add(20 * time.Hour), Routed: map[string]string{}},
		},
		{
			name: "the same proxy has room for other work",
			accounts: []proxyacct.Account{
				claude("main", proxyacct.StatusActive, 10, week(30, 60, 25*time.Hour), fable(70, 60, 20*time.Hour)),
				claude("team", proxyacct.StatusCooling, 10, week(100, 0, 30*time.Hour)),
			},
			want: proxyacct.Route{Lead: "main", Routed: map[string]string{"claude": "main"}},
		},
		{
			name: "when every Claude account is full the chip names the fullest and when the first frees",
			accounts: []proxyacct.Account{
				claude("main", proxyacct.StatusActive, 10, week(62, 60, 5*time.Hour)),
				claude("team", proxyacct.StatusCooling, 10, week(100, 0, 30*time.Hour)),
				codex("cx", 0, 40*time.Hour),
			},
			want: proxyacct.Route{Lead: "team", Full: true, FreesAt: now.Add(5 * time.Hour), Routed: map[string]string{"codex": "cx"}},
		},
		{
			name: "an account at its ceiling with no reset known is full, with no time invented",
			accounts: []proxyacct.Account{
				claude("main", proxyacct.StatusActive, 10, reading{kind: proxyacct.WindowWeek, percent: 70, ceiling: 60, noReset: true}),
			},
			want: proxyacct.Route{Lead: "main", Full: true, Routed: map[string]string{}},
		},
		{
			name: "when one full account does not say when it frees, no first time is given",
			accounts: []proxyacct.Account{
				claude("main", proxyacct.StatusActive, 10, reading{kind: proxyacct.WindowWeek, percent: 70, ceiling: 60, noReset: true}),
				claude("spare", proxyacct.StatusActive, 0, week(65, 60, 5*time.Hour)),
			},
			want: proxyacct.Route{Lead: "main", Full: true, Routed: map[string]string{}},
		},
		{
			name: "Codex accounts rank by preference alone, since the proxy reads only Anthropic headers",
			accounts: []proxyacct.Account{
				codex("late", 0, 90*time.Hour),
				codex("soon", 0, 10*time.Hour),
				codex("low", -1, 1*time.Hour),
			},
			want: proxyacct.Route{Lead: "late", Routed: map[string]string{"codex": "late"}},
		},
		{
			name: "a higher-preference Codex account wins",
			accounts: []proxyacct.Account{
				codex("plain", 0, 1*time.Hour),
				codex("liked", 10, 90*time.Hour),
			},
			want: proxyacct.Route{Lead: "liked", Routed: map[string]string{"codex": "liked"}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := proxyacct.Routing(c.accounts, now, c.fable); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("routing = %+v, want %+v", got, c.want)
			}
		})
	}
	if got := proxyacct.Routing([]proxyacct.Account{claude("off", proxyacct.StatusDisabled, 10)}, now, false); got.Lead != "" || got.Full || len(got.Routed) != 0 {
		t.Fatalf("a proxy with no live account routes nowhere: %+v", got)
	}
}
