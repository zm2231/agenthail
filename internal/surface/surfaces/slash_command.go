package surfaces

import (
	"strings"
	"unicode"
)

// slashCommandName returns the command a message invokes: its first word
// when that word is a slash and a name. A first word that holds another
// slash, such as a path, or a lone slash makes the message a prompt.
func slashCommandName(message string) (string, bool) {
	rest, ok := strings.CutPrefix(message, "/")
	if !ok {
		return "", false
	}
	name := rest
	if end := strings.IndexFunc(rest, unicode.IsSpace); end >= 0 {
		name = rest[:end]
	}
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return name, true
}
