package uiconf

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type colourUse struct {
	count int
	marks string
}

type statusColour struct {
	job   string
	uses  *regexp.Regexp
	files map[string]colourUse
}

func statusColourUses(accessor, role string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(
		`DesktopPalette\.%[1]s(Background|Line)?\b|PhonePalette\.%[1]s\b|PaletteTokens\.%[1]s\b|(RoleStyle|DesktopStyle\.role|\.role|RoleInlineStyle\.[A-Za-z]+)\(\s*\.%[2]s\b|roles\[\s*\.%[2]s\s*\]|TextRole\.%[2]s\b|\[TextRole\]\s*=\s*\[[^\]]*\.%[2]s\b|:\s*TextRole\??\s*=\s*\.%[2]s\b`,
		accessor, role))
}

var statusColours = map[string]statusColour{
	"amber": {
		job:  "something needs the person: a question or approval, a message that was not delivered, a decision",
		uses: statusColourUses("attention", "needsYou"),
		files: map[string]colourUse{
			"QuestionPagerView.swift":           {2, "questions not answered yet"},
			"RequestCard.swift":                 {1, "an end-of-turn ask waiting on the person"},
			"iOS/AgenthailIOSViews.swift":       {6, "needs-you rows and reasons, the waiting label, a goal needing the person, sign-in to fix, an end-of-turn ask"},
			"iOS/DecisionSections.swift":        {3, "a decision holding up other sessions, on its row and in its sheet, and the request waiting in the sheet"},
			"macOS/DecisionInboxPane.swift":     {2, "a decision holding up other sessions, and the request waiting in the card"},
			"macOS/DecisionList.swift":          {1, "a decision holding up other sessions, on its row"},
			"macOS/SidebarTiles.swift":          {1, "the count of decisions waiting on the person, on the Inbox tile"},
			"iOS/PhoneProjectTiles.swift":       {1, "the count of decisions waiting on the person, on a project tile"},
			"iOS/HolderPicker.swift":            {1, "a terminal must be picked before sending"},
			"iOS/QueueTailRows.swift":           {1, "a message that was not delivered"},
			"iOS/SessionControls.swift":         {1, "an undelivered message in the inbox"},
			"iOS/SessionListChrome.swift":       {1, "the needs-you ring"},
			"macOS/DesktopWindow.swift":         {10, "the needs-you waiting label, the delivery problem banner, undelivered messages, an end-of-turn ask, a goal that needs the person"},
			"macOS/GoalSection.swift":           {1, "a goal that needs the person"},
			"macOS/LeadReplacementBanner.swift": {3, "a lead replacement to decide"},
			"macOS/SidebarSessionRow.swift":     {1, "a needs-you row"},
			"macOS/StatusItems.swift":           {1, "the needs-you count"},
			"macOS/StuckMessages.swift":         {3, "messages that were not delivered"},
			"macOS/WindowChrome.swift":          {3, "the needs-you pill"},
		},
	},
	"blue": {
		job:  "live work: a turn running, background work, subagents working, a live call",
		uses: statusColourUses("working", "working"),
		files: map[string]colourUse{
			"iOS/AgenthailIOSViews.swift":    {6, "working and background-work labels, working subagents"},
			"iOS/SessionListChrome.swift":    {5, "the working and finishing rings, a live call"},
			"iOS/UsageAccountsSheet.swift":   {1, "sessions working on an account"},
			"macOS/DesktopWindow.swift":      {2, "working subagents and the finishing ring"},
			"macOS/StatusItems.swift":        {1, "the working count"},
			"macOS/SubagentsSection.swift":   {1, "a working subagent"},
			"macOS/DesktopGlass.swift":       {1, "the working role a selected row must stay readable for"},
			"macOS/UsageAccountsPanel.swift": {1, "sessions working on an account"},
			"macOS/WorkingSpinner.swift":     {1, "the working spinner"},
		},
	},
	"status dot": {
		job:  "a session's status dot, which picks amber for needs-you, blue for working and green for finished-unseen",
		uses: regexp.MustCompile(`DesktopPalette\.statusColor\b`),
		files: map[string]colourUse{
			"macOS/DesktopWindow.swift": {1, "the sidebar row's status dot"},
			"macOS/WindowChrome.swift":  {1, "the tab's status dot"},
		},
	},
	"green": {
		job:  "finished or passed: a done turn, added lines, a healthy connection or grant",
		uses: statusColourUses("done", "done"),
		files: map[string]colourUse{
			"iOS/CompactActivity.swift":      {1, "added lines"},
			"iOS/DiffView.swift":             {1, "added lines"},
			"macOS/ConnectionControls.swift": {1, "connected"},
			"macOS/DesktopSettings.swift":    {2, "connected, healthy hosts"},
			"macOS/DiffView.swift":           {1, "added lines"},
			"macOS/OverviewSettings.swift":   {2, "healthy surfaces, granted permissions"},
			"macOS/ToolRunView.swift":        {1, "added lines"},
		},
	},
	"project": {
		job:  "which project something belongs to, on the project's tile mark; never a state",
		uses: regexp.MustCompile(`DesktopPalette\.project\(|PhonePalette\.project\(|PaletteTokens\.projects\[`),
		files: map[string]colourUse{
			"macOS/SidebarTiles.swift":    {1, "a project tile's letter mark"},
			"iOS/PhoneProjectTiles.swift": {1, "a project tile's letter mark"},
		},
	},
}

var paletteDefinitions = map[string]bool{
	"PaletteTokens.swift":        true,
	"iOS/PhonePalette.swift":     true,
	"macOS/DesktopPalette.swift": true,
}

var testDirectories = map[string]bool{"tests": true, "iOSTests": true, "iOSUITests": true}

var paletteAlias = regexp.MustCompile(`=\s*(DesktopPalette|PhonePalette|PaletteTokens)\s*(\.self)?\s*$|(DesktopPalette|PhonePalette|PaletteTokens)\.self|typealias\s+\w+\s*=\s*(DesktopPalette|PhonePalette|PaletteTokens)\b`)

func nativeSwiftSources(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "native")
	sources := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if testDirectories[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".swift") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		sources[filepath.ToSlash(rel)] = string(source)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sources
}

func checkStatusColourUses(sources map[string]string, colours map[string]statusColour) []string {
	var problems []string
	for name, colour := range colours {
		found := map[string]int{}
		for file, source := range sources {
			if paletteDefinitions[file] {
				continue
			}
			for _, line := range strings.Split(source, "\n") {
				if paletteAlias.MatchString(line) {
					problems = append(problems, fmt.Sprintf("%s aliases a palette (%q); reach status colours only by name so their uses can be counted", file, strings.TrimSpace(line)))
				}
			}
			if n := len(colour.uses.FindAllStringIndex(source, -1)); n > 0 {
				found[file] = n
			}
		}
		for file, n := range found {
			listed, ok := colour.files[file]
			switch {
			case !ok:
				problems = append(problems, fmt.Sprintf("%s uses %s %d times; its one job is: %s. Use it only for that and list the file with what it marks, or use a neutral text role", file, name, n, colour.job))
			case n != listed.count:
				problems = append(problems, fmt.Sprintf("%s uses %s %d times, listed as %d (%s). Each use must mark: %s. Update the count only if the new use does", file, name, n, listed.count, listed.marks, colour.job))
			}
		}
		for file := range colour.files {
			if found[file] == 0 {
				problems = append(problems, fmt.Sprintf("%s is listed for %s but no longer uses it; remove it from the list", file, name))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

func TestEachStatusColourKeepsItsOneJob(t *testing.T) {
	for _, problem := range checkStatusColourUses(nativeSwiftSources(t), statusColours) {
		t.Error(problem)
	}
}

func TestTheColourUseCheckCatchesEachWayAColourIsReached(t *testing.T) {
	listed := map[string]statusColour{"amber": {job: "needs you", uses: statusColourUses("attention", "needsYou"), files: map[string]colourUse{"iOS/A.swift": {1, "a row"}}}}
	cases := map[string]map[string]string{
		"a second use in a listed file": {"iOS/A.swift": "PhonePalette.attention\nPhonePalette.attention"},
		"a new shared file":             {"iOS/A.swift": "PhonePalette.attention", "Shared.swift": "RoleStyle(.needsYou)"},
		"a new macOS file":              {"iOS/A.swift": "PhonePalette.attention", "macOS/B.swift": "DesktopPalette.attentionBackground"},
		"the role table":                {"iOS/A.swift": "PhonePalette.attention", "macOS/C.swift": "PaletteTokens.roles[.needsYou]"},
		"a role passed by name":         {"iOS/A.swift": "PhonePalette.attention", "macOS/D.swift": "let role = TextRole.needsYou"},
		"the desktop role style":        {"iOS/A.swift": "PhonePalette.attention", "macOS/E.swift": "DesktopStyle.role(.needsYou)"},
		"an inline role":                {"iOS/A.swift": "PhonePalette.attention", "F.swift": "RoleInlineStyle.color(.needsYou, colors)"},
		"a palette alias":               {"iOS/A.swift": "PhonePalette.attention", "macOS/G.swift": "let p = DesktopPalette.self"},
		"a role collection":             {"iOS/A.swift": "PhonePalette.attention", "macOS/H.swift": "static let roles: [TextRole] = [.body,\n    .needsYou]"},
		"a typed role":                  {"iOS/A.swift": "PhonePalette.attention", "macOS/I.swift": "let role: TextRole = .needsYou"},
		"the last use removed":          {"iOS/A.swift": "Text(\"x\")"},
	}
	for name, sources := range cases {
		if problems := checkStatusColourUses(sources, listed); len(problems) == 0 {
			t.Errorf("%s passed the check", name)
		}
	}
	if problems := checkStatusColourUses(map[string]string{"iOS/A.swift": "PhonePalette.attention"}, listed); len(problems) != 0 {
		t.Errorf("the listed use failed: %v", problems)
	}
	dots := map[string]statusColour{"status dot": statusColours["status dot"]}
	extra := map[string]string{"macOS/DesktopWindow.swift": "DesktopPalette.statusColor(s)", "macOS/WindowChrome.swift": "DesktopPalette.statusColor(s)", "macOS/J.swift": "DesktopPalette.statusColor(s)"}
	if problems := checkStatusColourUses(extra, dots); len(problems) == 0 {
		t.Error("a new status dot passed the check")
	}
}

func TestOnlyTestDirectoriesAreSkipped(t *testing.T) {
	for _, name := range []string{"testsupport", "testing", "iOS", "macOS"} {
		if testDirectories[name] {
			t.Errorf("%s would be skipped", name)
		}
	}
}
