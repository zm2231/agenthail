package surface

import "testing"

func TestCodexInjectedContextIsOnlyCodexFragments(t *testing.T) {
	agents := "# AGENTS.md instructions for /work\n\n<INSTRUCTIONS>\nBe brief.\n\n## Tests\nRun them.\n</INSTRUCTIONS>"
	environment := "<environment_context>\n  <cwd>/work</cwd>\n</environment_context>"
	for _, test := range []struct {
		text string
		want bool
	}{
		{agents, true},
		{environment, true},
		{agents + "\n\n" + environment, true},
		{"  " + environment + "\n", true},
		{"# AGENTS.md instructions for /work\n\n<INSTRUCTIONS>\nBe brief.\n\n## My request:\nFix the app.\n</INSTRUCTIONS>", false},
		{"# AGENTS.md instructions for /project\n\n## My request:\nFix the app.", false},
		{agents + "\n\nFix the build.", false},
		{environment + "\nRun the tests.", false},
		{"What does <environment_context> mean?", false},
		{"<environment_context>never closed", false},
		{"", false},
	} {
		if got := IsCodexInjectedContext(test.text); got != test.want {
			t.Errorf("IsCodexInjectedContext(%q)=%v, want %v", test.text, got, test.want)
		}
	}
}
