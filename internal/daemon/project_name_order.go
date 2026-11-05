package daemon

import "strings"

func projectNameBefore(aName, aID, bName, bID string) bool {
	if folded, other := strings.ToLower(aName), strings.ToLower(bName); folded != other {
		return folded < other
	}
	if aName != bName {
		return aName < bName
	}
	return aID < bID
}
