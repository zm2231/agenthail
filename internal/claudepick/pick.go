package claudepick

import (
	"github.com/zm2231/agenthail/internal/surface"
)

// Pick is where a new Claude session starts. Root is empty for the primary root.
type Pick struct {
	Root    string `json:"root"`
	Name    string `json:"name"`
	Account string `json:"account,omitempty"`
	Reason  string `json:"reason"`
}

type Origin struct {
	CreatorTranscript  string
	PreviousTranscript string
}

// Place never reads usage; an explicit root is the caller's to apply first.
func Place(roots []surface.ClaudeConfigRoot, origin Origin, readDefault func() (string, error)) (Pick, error) {
	if root, ok := transcriptRoot(roots, origin.CreatorTranscript); ok {
		return pick(root, "the root of the session that created it"), nil
	}
	if root, ok := transcriptRoot(roots, origin.PreviousTranscript); ok {
		return pick(root, "the root of the lead it replaces"), nil
	}
	defaultRoot, err := readDefault()
	if err != nil {
		return Pick{}, err
	}
	primary, found := surface.ClaudeConfigRoot{}, false
	for _, root := range roots {
		if defaultRoot != "" && root.Name == defaultRoot {
			return pick(root, "your default root for new sessions"), nil
		}
		if root.Primary && !found {
			primary, found = root, true
		}
	}
	if !found && len(roots) > 0 {
		primary = roots[0]
	}
	if defaultRoot != "" {
		return pick(primary, "the primary root; the default root "+defaultRoot+" is not configured"), nil
	}
	return pick(primary, "the primary root"), nil
}

func transcriptRoot(roots []surface.ClaudeConfigRoot, transcript string) (surface.ClaudeConfigRoot, bool) {
	view := surface.ClaudeSessionConfigRoot(roots, transcript)
	if view == nil {
		return surface.ClaudeConfigRoot{}, false
	}
	for _, root := range roots {
		if root.Root == view.Root {
			return root, true
		}
	}
	return surface.ClaudeConfigRoot{}, false
}

func pick(root surface.ClaudeConfigRoot, reason string) Pick {
	chosen := Pick{Root: root.Root, Name: root.Name, Reason: reason}
	if root.Primary {
		chosen.Root = ""
	}
	if root.SignedInAs != nil {
		chosen.Account = root.SignedInAs.Email
	}
	return chosen
}
