package uiconf

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var fileURLUse = regexp.MustCompile(`fileURLWithPath|URL\(\s*filePath:`)

var localFileURLs = map[string]colourUse{
	"GenerateIcon.swift":           {1, "writes the generated icon file"},
	"macOS/AgenthailProcess.swift": {2, "the executable the Mac app launches"},
	"macOS/DesktopWindow.swift":    {1, "opens a file on this Mac"},
	"macOS/NewSession.swift":       {1, "the folder an open panel starts in"},
	"iOS/ScrollJumpProbe.swift":    {1, "a debug probe's file in the app's temporary folder"},
	"iOS/SessionPreview.swift":     {1, "reads a preview wallpaper file"},
}

func checkFileURLUses(sources map[string]string, listed map[string]colourUse) []string {
	var problems []string
	found := map[string]int{}
	for file, source := range sources {
		if strings.HasPrefix(file, "Packages/") {
			continue
		}
		if n := len(fileURLUse.FindAllStringIndex(source, -1)); n > 0 {
			found[file] = n
		}
	}
	for file, n := range found {
		entry, ok := listed[file]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s builds a file URL %d times. A file URL from a path can check the file system on the main thread; name a session's or checkout's folder with PathNames, or list the file here if it reads or writes a file on this device", file, n))
		case n != entry.count:
			problems = append(problems, fmt.Sprintf("%s builds a file URL %d times, listed as %d (%s). Name folders with PathNames; update the count only for another file this device reads or writes", file, n, entry.count, entry.marks))
		}
	}
	for file := range listed {
		if found[file] == 0 {
			problems = append(problems, fmt.Sprintf("%s is listed as building a file URL but no longer does; remove it from the list", file))
		}
	}
	sort.Strings(problems)
	return problems
}

func TestFolderNamesNeverReachTheFileSystem(t *testing.T) {
	for _, problem := range checkFileURLUses(nativeSwiftSources(t), localFileURLs) {
		t.Error(problem)
	}
}

func TestTheFileURLCheckCatchesANewUse(t *testing.T) {
	listed := map[string]colourUse{"macOS/A.swift": {1, "opens a file"}}
	cases := map[string]map[string]string{
		"a folder label in a view":      {"macOS/A.swift": "URL(fileURLWithPath: p)", "iOS/B.swift": "Text(URL(fileURLWithPath: cwd).lastPathComponent)"},
		"a shared tree label":           {"macOS/A.swift": "URL(fileURLWithPath: p)", "C.swift": "URL(filePath: path).pathComponents"},
		"a second use in a listed file": {"macOS/A.swift": "URL(fileURLWithPath: p)\nURL(fileURLWithPath: q, isDirectory: true)"},
		"the last use removed":          {"macOS/A.swift": "PathNames.last(p)"},
	}
	for name, sources := range cases {
		if problems := checkFileURLUses(sources, listed); len(problems) == 0 {
			t.Errorf("%s passed the check", name)
		}
	}
	allowed := map[string]string{"macOS/A.swift": "URL(fileURLWithPath: p)", "Packages/Vendor/D.swift": "URL(fileURLWithPath: p)"}
	if problems := checkFileURLUses(allowed, listed); len(problems) != 0 {
		t.Errorf("the listed use failed: %v", problems)
	}
}
