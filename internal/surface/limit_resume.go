package surface

import (
	"regexp"
	"strings"
)

// LimitResumeText is what Agenthail tells a session a usage limit stopped,
// once the limit resets, to continue the work it was doing.
const LimitResumeText = "Your usage limit has reset. Continue the work that was interrupted."

var noticeEnvelope = regexp.MustCompile(`(?s)^\s*<agenthail-notice kind="([a-z-]+)">(.*?)</agenthail-notice>\s*$`)

// LimitResumeNotice is the message that resumes a session after its usage
// limit resets. Its envelope lets every reader of the transcript show it as
// Agenthail's notice rather than as something the person typed.
func LimitResumeNotice() string {
	return `<agenthail-notice kind="limit-resume">` + LimitResumeText + `</agenthail-notice>`
}

// ParseNotice reports the kind and text of an Agenthail notice.
func ParseNotice(text string) (kind, body string, ok bool) {
	match := noticeEnvelope.FindStringSubmatch(text)
	if match == nil {
		return "", "", false
	}
	return match[1], strings.TrimSpace(match[2]), true
}
