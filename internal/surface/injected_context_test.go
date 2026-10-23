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
		{"<codex_internal_context source=\"goal\">\nContinue working toward the active thread goal.\n</codex_internal_context>", true},
		{"<external_codex_apps_open_page>{\"page_id\":null}</external_codex_apps_open_page>", true},
		{"<recommended_plugins>\n- Box\n</recommended_plugins>\n" + environment, true},
		{"<codex_internal_context source=\"goal\">\nContinue.\n</codex_internal_context>\nAlso fix the tests.", false},
		{"", false},
	} {
		if got := IsCodexInjectedContext(test.text); got != test.want {
			t.Errorf("IsCodexInjectedContext(%q)=%v, want %v", test.text, got, test.want)
		}
	}
}
