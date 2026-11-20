package surface

import (
	"regexp"
	"strings"
)

// LimitResumeText is what Agenthail tells a session a usage limit stopped,
// once the limit resets, to continue the work it was doing.
const LimitResumeText = "Your usage limit has reset. Continue the work that was interrupted."

// NoticeLimitResume is the kind of the notice that resumes a session after its
// usage limit resets.
const NoticeLimitResume = "limit-resume"

var noticeEnvelope = regexp.MustCompile(`(?s)^\s*<agenthail-notice kind="([a-z-]+)">(.*?)</agenthail-notice>\s*$`)

// LimitResumeNotice is the message that resumes a session after its usage
// limit resets. Its envelope lets every reader of the transcript show it as
// Agenthail's notice rather than as something the person typed.
func LimitResumeNotice() string {
	return `<agenthail-notice kind="` + NoticeLimitResume + `">` + LimitResumeText + `</agenthail-notice>`
}

// NoticePrefix opens every notice the daemon writes from a request's fields.
// Only a turn that reaches the session unwrapped and without the person's
// origin is Agenthail's; an agent's text is always wrapped.
const NoticePrefix = "[agenthail] "

func IsAgenthailNotice(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, NoticePrefix) || strings.HasPrefix(text, "[agenthail delivery ")
}

const CLIPeerPreamble = "Sent with agenthail send from outside any session; no one reads replies to it.\n\n"

const OriginAgenthailNotice = "agenthail-notice"

// ParseNotice reports the kind and text of an Agenthail notice.
func ParseNotice(text string) (kind, body string, ok bool) {
	match := noticeEnvelope.FindStringSubmatch(text)
	if match == nil {
		return "", "", false
	}
	return match[1], strings.TrimSpace(match[2]), true
}

func NoticeTitle(kind string) string {
	if kind == NoticeLimitResume {
		return "Usage limit reset"
	}
	return "Agenthail notice"
}

func CLINotice(text string) (TimelineItem, bool) {
	body := strings.TrimSpace(text)
	body = strings.TrimSpace(strings.TrimPrefix(body, strings.TrimSpace(CLIPeerPreamble)))
	kind, notice, ok := ParseNotice(body)
	if !ok {
		return TimelineItem{}, false
	}
	return TimelineItem{Kind: "event", Role: "system", Origin: OriginAgenthailNotice, Title: NoticeTitle(kind), Text: notice}, true
}
