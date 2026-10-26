package uiconf

import "strings"

func (d Device) rank(target string) int {
	switch {
	case target == "":
		return 1
	case target == d.Class:
		return 2
	case d.Name != "" && target == "device:"+d.Name:
		return 3
	}
	return 0
}

// SetFor also removes the lines that would outrank the assignments on device.
func SetFor(text string, device Device, assignments ...Assignment) (string, []string) {
	text = Set(text, assignments...)
	var cleared []string
	for _, a := range assignments {
		var removed []string
		text, removed = unmask(text, device, a.Key, a.Device)
		cleared = append(cleared, removed...)
	}
	return text, cleared
}

// UnsetFor also removes the lines that would outrank the removed ones on device.
func UnsetFor(text string, device Device, names ...string) (string, []string) {
	asked := map[string]bool{}
	for _, name := range names {
		asked[name] = true
	}
	var cleared []string
	for _, name := range names {
		key, target, _ := strings.Cut(name, "@")
		text = Unset(text, key, target)
		var removed []string
		text, removed = unmask(text, device, key, target)
		for _, r := range removed {
			if !asked[r] {
				cleared = append(cleared, r)
			}
		}
	}
	return text, cleared
}

func unmask(text string, device Device, key, target string) (string, []string) {
	floor := device.rank(target)
	if floor == 0 {
		return text, nil
	}
	var removed []string
	for _, raw := range strings.Split(text, "\n") {
		l, _ := splitLine(raw)
		if !l.assignment || l.key != key || device.rank(l.device) <= floor {
			continue
		}
		if name := key + "@" + l.device; !contains(removed, name) {
			removed = append(removed, name)
		}
	}
	for _, name := range removed {
		_, d, _ := strings.Cut(name, "@")
		text = Unset(text, key, d)
	}
	return text, removed
}
