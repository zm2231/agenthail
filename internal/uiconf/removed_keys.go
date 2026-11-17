package uiconf

var removedKeys = map[string]string{
	"glass.sidebar.opacity": "glass.opacity, which now tints every pane",
	"glass.chrome.opacity":  "glass.opacity, which now tints every pane",
	"titlebar.tabs.preview": "titlebar.tabs; every session you open gets a tab, and the least recently used one closes when the strip is full",
}

var retiredKeys = map[string]bool{
	"scrim.feather": true,
}

func unknownKey(key string) string {
	if retiredKeys[key] {
		return key + " was retired and has no effect; the reading column keeps text readable"
	}
	if replacement, removed := removedKeys[key]; removed {
		return key + " was removed; use " + replacement
	}
	return "unknown key " + key
}
