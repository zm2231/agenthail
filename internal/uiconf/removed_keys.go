package uiconf

var removedKeys = map[string]string{
	"glass.sidebar.opacity": "glass.opacity, which now tints every pane",
	"glass.chrome.opacity":  "glass.opacity, which now tints every pane",
	"titlebar.tabs.preview": "titlebar.tabs; every session you open gets a tab, and the least recently used one closes when the strip is full",
}

func unknownKey(key string) string {
	if replacement, removed := removedKeys[key]; removed {
		return key + " was removed; use " + replacement
	}
	return "unknown key " + key
}
