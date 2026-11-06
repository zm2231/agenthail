package uiconf

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type statusColour struct {
	job   string
	uses  *regexp.Regexp
	files map[string]string
}

var statusColours = map[string]statusColour{
	"amber": {
		job:  "something needs the person: a question or approval, a message that was not delivered, a decision",
		uses: regexp.MustCompile(`DesktopPalette\.attention(Background|Line)?\b|PhonePalette\.attention\b|RoleStyle\(\.needsYou\)|\.role\(\.needsYou\)`),
		files: map[string]string{
			"QuestionPagerView.swift":           "questions not answered yet",
			"iOS/AgenthailIOSViews.swift":       "needs-you rows, reasons, waiting label, goal needing the person, sign-in to fix",
			"iOS/HolderPicker.swift":            "a terminal must be picked before sending",
			"iOS/QueueTailRows.swift":           "a message that was not delivered",
			"iOS/SessionControls.swift":         "an undelivered message in the inbox",
			"iOS/SessionListChrome.swift":       "the needs-you ring",
			"macOS/DesktopWindow.swift":         "needs-you rows and undelivered messages",
			"macOS/GoalSection.swift":           "a goal that needs the person",
			"macOS/LeadReplacementBanner.swift": "a lead replacement to decide",
			"macOS/StatusTail.swift":            "the needs-you count",
			"macOS/StuckMessages.swift":         "messages that were not delivered",
			"macOS/WindowChrome.swift":          "the needs-you count",
		},
	},
	"blue": {
		job:  "live work: a turn running, background work, subagents working, a live call",
		uses: regexp.MustCompile(`DesktopPalette\.working\b|PhonePalette\.working\b|RoleStyle\(\.working\)|\.role\(\.working\)`),
		files: map[string]string{
			"iOS/AgenthailIOSViews.swift":    "working and background-work labels, working subagents",
			"iOS/SessionListChrome.swift":    "the working ring and a live call",
			"iOS/UsageAccountsSheet.swift":   "sessions working on an account",
			"macOS/DesktopWindow.swift":      "working subagents and the finishing ring",
			"macOS/StatusTail.swift":         "the working count",
			"macOS/SubagentsSection.swift":   "a working subagent",
			"macOS/UsageAccountsPanel.swift": "sessions working on an account",
			"macOS/WorkingSpinner.swift":     "the working spinner",
		},
	},
	"green": {
		job:  "finished or passed: a done turn, added lines, a healthy connection or grant",
		uses: regexp.MustCompile(`DesktopPalette\.done\b|PhonePalette\.done\b|RoleStyle\(\.done\)|\.role\(\.done\)`),
		files: map[string]string{
			"iOS/CompactActivity.swift":      "added lines",
			"iOS/DiffView.swift":             "added lines",
			"macOS/ConnectionControls.swift": "connected",
			"macOS/DesktopSettings.swift":    "connected and healthy hosts",
			"macOS/DiffView.swift":           "added lines",
			"macOS/OverviewSettings.swift":   "healthy surfaces and granted permissions",
			"macOS/ToolRunView.swift":        "added lines",
		},
	},
}

func TestEachStatusColourKeepsItsOneJob(t *testing.T) {
	root := filepath.Join("..", "..", "native")
	found := map[string]map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if entry.IsDir() {
			if strings.HasPrefix(rel, "tests") || strings.HasSuffix(rel, "Tests") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".swift") || strings.HasSuffix(path, "DesktopPalette.swift") || strings.HasSuffix(path, "PhonePalette.swift") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for name, colour := range statusColours {
			if colour.uses.Match(source) {
				if found[name] == nil {
					found[name] = map[string]bool{}
				}
				found[name][filepath.ToSlash(rel)] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, colour := range statusColours {
		var extra, stale []string
		for file := range found[name] {
			if _, listed := colour.files[file]; !listed {
				extra = append(extra, file)
			}
		}
		for file := range colour.files {
			if !found[name][file] {
				stale = append(stale, file)
			}
		}
		sort.Strings(extra)
		sort.Strings(stale)
		for _, file := range extra {
			t.Errorf("%s uses %s, whose one job is: %s. Use it only for that and list the file here with what it marks, or use a neutral text role", file, name, colour.job)
		}
		for _, file := range stale {
			t.Errorf("%s is listed for %s but no longer uses it; remove it from the list", file, name)
		}
	}
}
