package github

import (
	"regexp"
	"strconv"
	"strings"
)

const commandStart = "(?:^|[;&|({`\\n]|\\b(?:do|then|else)\\s)\\s*(?:(?:\\w+=\\S*|env|time|nohup|timeout\\s+\\S+)\\s+)*"

var (
	createWord     = regexp.MustCompile(`\bgh\s+pr\s+create\b`)
	createRun      = regexp.MustCompile(commandStart + `gh\s+pr\s+create`)
	shellWrapper   = regexp.MustCompile(`^\s*(?:\S*/)?(?:ba|z|da|k)?sh\s+-l?c\s+([\s\S]*)$`)
	wordPiece      = regexp.MustCompile(`"((?:\\[\s\S]|[^"\\])*)"|'([^']*)'|\\([\s\S])|(\S)`)
	doubleEscape   = regexp.MustCompile("\\\\([\"\\\\$`])")
	heredocStart   = regexp.MustCompile(`<<-?[ \t]*(['"]?)(\w+)`)
	separator      = regexp.MustCompile(`&&|\|\||[;|\n]`)
	listsPulls     = regexp.MustCompile(`\bgh\s+(?:pr\s+(?:list|status)|search\s+prs|api)\b`)
	urlLine        = regexp.MustCompile(`(?m)^[ \t]*(https?://\S+)[ \t\r]*$`)
	wordEnd        = " \t\n;&|)"
	commentAfter   = " \t\n\r\f\v;&|("
	numberBefore   = " \t\n\r\f\v#/=(\"'"
	numberAfterSet = " \t\n\r\f\v);&|/\"'"
)

// A URL the script also names existed before it ran; more URLs than create
// commands means something else printed some, so none are taken.
func CreatedBy(command, output string) []Ref {
	if !createWord.MatchString(command) {
		return nil
	}
	script := shellScript(command)
	code := shellCode(script)
	creates := createCount(code)
	if creates == 0 {
		return nil
	}
	var otherCode []string
	for _, part := range separator.Split(code, -1) {
		if createCount(part) == 0 {
			otherCode = append(otherCode, part)
		}
	}
	if listsPulls.MatchString(strings.Join(otherCode, "\n")) {
		return nil
	}
	var otherSegments []string
	for _, segment := range shellSegments(script) {
		if createCount(shellCode(segment)) == 0 {
			otherSegments = append(otherSegments, segment)
		}
	}
	otherScript := strings.Join(otherSegments, "\n")
	var created []Ref
	seen := map[Ref]bool{}
	for _, match := range urlLine.FindAllStringSubmatch(output, -1) {
		ref, ok := ParseURL(match[1])
		if !ok || seen[ref] || names(otherScript, ref.Number) {
			continue
		}
		seen[ref] = true
		created = append(created, ref)
	}
	if len(created) > creates {
		return nil
	}
	return created
}

func createCount(code string) int {
	count := 0
	for _, loc := range createRun.FindAllStringIndex(code, -1) {
		if loc[1] == len(code) || strings.IndexByte(wordEnd, code[loc[1]]) >= 0 {
			count++
		}
	}
	return count
}

func shellScript(input string) string {
	match := shellWrapper.FindStringSubmatch(input)
	if match == nil || match[1] == "" || match[1][0] != '"' && match[1][0] != '\'' {
		return input
	}
	var out strings.Builder
	for _, piece := range wordPiece.FindAllStringSubmatchIndex(match[1], -1) {
		text := func(group int) (string, bool) {
			if piece[2*group] < 0 {
				return "", false
			}
			return match[1][piece[2*group]:piece[2*group+1]], true
		}
		if double, ok := text(1); ok {
			out.WriteString(doubleEscape.ReplaceAllString(double, "$1"))
		} else if single, ok := text(2); ok {
			out.WriteString(single)
		} else if escaped, ok := text(3); ok {
			out.WriteString(escaped)
		} else if bare, ok := text(4); ok {
			out.WriteString(bare)
		}
	}
	return out.String()
}

func shellSegments(script string) []string {
	var segments []string
	var current strings.Builder
	quote := byte(0)
	for i := 0; i < len(script); i++ {
		c := script[i]
		if quote != 0 {
			current.WriteByte(c)
			if c == '\\' && quote == '"' && i+1 < len(script) {
				i++
				current.WriteByte(script[i])
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
		}
		if c == '\\' {
			current.WriteByte(c)
			if i+1 < len(script) {
				i++
				current.WriteByte(script[i])
			}
			continue
		}
		pair := script[i:min(i+2, len(script))]
		if pair == "&&" || pair == "||" || c == ';' || c == '|' || c == '\n' {
			segments = append(segments, current.String())
			current.Reset()
			if pair == "&&" || pair == "||" {
				i++
			}
			continue
		}
		current.WriteByte(c)
	}
	return append(segments, current.String())
}

func shellCode(script string) string {
	return stripQuotesAndComments(stripHeredocs(script))
}

func stripHeredocs(s string) string {
	var out strings.Builder
	for {
		loc := heredocStart.FindStringSubmatchIndex(s)
		if loc == nil {
			out.WriteString(s)
			return out.String()
		}
		quote, word, rest := s[loc[2]:loc[3]], s[loc[4]:loc[5]], s[loc[1]:]
		if !strings.HasPrefix(rest, quote) {
			out.WriteString(s[:loc[1]])
			s = rest
			continue
		}
		rest = rest[len(quote):]
		newline := strings.IndexByte(rest, '\n')
		if newline < 0 {
			out.WriteString(s[:loc[1]])
			s = s[loc[1]:]
			continue
		}
		out.WriteString(s[:loc[0]])
		out.WriteString("<<")
		body := rest[newline+1:]
		end := len(body)
		for offset := 0; ; {
			lineEnd := strings.IndexByte(body[offset:], '\n')
			line := body[offset:]
			if lineEnd >= 0 {
				line = body[offset : offset+lineEnd]
			}
			if strings.Trim(line, " \t") == word {
				end = offset + len(line)
				break
			}
			if lineEnd < 0 {
				break
			}
			offset += lineEnd + 1
		}
		s = body[end:]
	}
}

func stripQuotesAndComments(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'':
			if j := strings.IndexByte(s[i+1:], '\''); j >= 0 {
				out.WriteString("''")
				i += j + 2
				continue
			}
		case c == '"':
			if j := closingDouble(s, i+1); j >= 0 {
				out.WriteString("''")
				i = j + 1
				continue
			}
		case c == '#' && (i == 0 || strings.IndexByte(commentAfter, s[i-1]) >= 0):
			j := strings.IndexByte(s[i:], '\n')
			if j < 0 {
				j = len(s) - i
			}
			out.WriteString("''")
			i += j
			continue
		}
		out.WriteByte(c)
		i++
	}
	return out.String()
}

func closingDouble(s string, from int) int {
	for k := from; k < len(s); k++ {
		switch s[k] {
		case '\\':
			k++
		case '"':
			return k
		}
	}
	return -1
}

func names(script string, number int) bool {
	digits := strconv.Itoa(number)
	for offset := 0; ; {
		at := strings.Index(script[offset:], digits)
		if at < 0 {
			return false
		}
		start, end := offset+at, offset+at+len(digits)
		before := start == 0 || strings.IndexByte(numberBefore, script[start-1]) >= 0
		after := end == len(script) || strings.IndexByte(numberAfterSet, script[end]) >= 0
		if before && after {
			return true
		}
		offset = start + 1
	}
}
