package daemon

import (
	"strings"
	"testing"
)

func TestExtensionPortPoolsMoveOnlyToRangesThatFit(t *testing.T) {
	for _, tc := range []struct {
		name           string
		env            map[string]string
		dashboard      int
		local, remote  int
		problemMention string
	}{
		{name: "unset", local: 0, remote: 0},
		{name: "both moved", env: map[string]string{extensionPortBaseEnv: "27420", remoteExtensionPortBaseEnv: "27620"}, dashboard: 7412, local: 27420, remote: 27620},
		{name: "not a number", env: map[string]string{extensionPortBaseEnv: "lots"}, local: 0, remote: 0, problemMention: "not a number"},
		{name: "past the last port", env: map[string]string{remoteExtensionPortBaseEnv: "65400"}, local: 0, remote: 0, problemMention: "up to 65535"},
		{name: "below 1024", env: map[string]string{extensionPortBaseEnv: "80"}, local: 0, remote: 0, problemMention: "from 1024"},
		{name: "over the dashboard", env: map[string]string{extensionPortBaseEnv: "27400"}, dashboard: 27412, local: 0, remote: 0, problemMention: "dashboard's port 27412"},
		{name: "remote over local", env: map[string]string{extensionPortBaseEnv: "27420", remoteExtensionPortBaseEnv: "27500"}, local: 27420, remote: 0, problemMention: "overlaps"},
		{name: "only one pinned", env: map[string]string{extensionPortBaseEnv: "7600"}, local: 7600, remote: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local, remote, problems := extensionPortBases(func(name string) string { return tc.env[name] }, tc.dashboard)
			if local != tc.local || remote != tc.remote {
				t.Fatalf("pools start at %d and %d, want %d and %d (%v)", local, remote, tc.local, tc.remote, problems)
			}
			if tc.problemMention == "" && len(problems) != 0 {
				t.Fatalf("unexpected problems %v", problems)
			}
			if tc.problemMention != "" && (len(problems) != 1 || !strings.Contains(problems[0], tc.problemMention)) {
				t.Fatalf("problems %v should say %q", problems, tc.problemMention)
			}
		})
	}
}
