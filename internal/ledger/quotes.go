package ledger

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	QuoteMinRunes = 40
	QuoteReplies  = 5
	QuoteReplyMax = 64 << 10
	quoteTextMax  = 256 << 10
)

// Start and End are byte offsets into the message text as the ledger stores it.
type Quote struct {
	Start       int    `json:"start"`
	End         int    `json:"end"`
	ReplyItemID string `json:"replyItemId,omitempty"`
}

type QuoteSource struct {
	ItemID string
	TS     string
	Text   string
}

type normalized struct {
	text       string
	original   int
	starts     []int32
	runeOffset []int32
}

func normalize(text string) normalized {
	var b strings.Builder
	n := normalized{original: len(text)}
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		n.starts = append(n.starts, int32(i))
		i += size
		if unicode.IsSpace(r) {
			for i < len(text) {
				next, nextSize := utf8.DecodeRuneInString(text[i:])
				if !unicode.IsSpace(next) {
					break
				}
				i += nextSize
			}
			r = ' '
		}
		n.runeOffset = append(n.runeOffset, int32(b.Len()))
		b.WriteRune(r)
	}
	n.runeOffset = append(n.runeOffset, int32(b.Len()))
	n.text = b.String()
	return n
}

func (n normalized) runes() int { return len(n.starts) }

func (n normalized) rune(i int) string { return n.text[n.runeOffset[i]:n.runeOffset[i+1]] }

func (n normalized) window(i int) string {
	return n.text[n.runeOffset[i]:n.runeOffset[i+QuoteMinRunes]]
}

func (n normalized) end(i int) int {
	if i+1 < len(n.starts) {
		return int(n.starts[i+1])
	}
	return n.original
}

func head(text string, max int) string {
	if len(text) <= max {
		return text
	}
	for max > 0 && !utf8.RuneStart(text[max]) {
		max--
	}
	return text[:max]
}

type windowAt struct {
	source int32
	rune   int32
}

// FindQuotes returns the spans of text that are quoted rather than typed: runs of lines starting with "> ",
// and stretches of at least QuoteMinRunes runes that equal, whitespace aside, a stretch of one reply.
// Replies come newest first and a stretch names the reply it is part of. Only the first QuoteReplyMax
// bytes of a reply and quoteTextMax bytes of the text are compared.
func FindQuotes(text string, replies []QuoteSource) []Quote {
	sources := make([]normalized, len(replies))
	windows := map[string]windowAt{}
	for i, reply := range replies {
		sources[i] = normalize(head(reply.Text, QuoteReplyMax))
		for w := 0; w+QuoteMinRunes <= sources[i].runes(); w++ {
			if _, seen := windows[sources[i].window(w)]; !seen {
				windows[sources[i].window(w)] = windowAt{source: int32(i), rune: int32(w)}
			}
		}
	}
	quotes := quotedLines(text, replies, sources)
	message := normalize(head(text, quoteTextMax))
	for i := 0; i+QuoteMinRunes <= message.runes(); {
		at, ok := windows[message.window(i)]
		if !ok {
			i++
			continue
		}
		source := sources[at.source]
		end, from := i+QuoteMinRunes, int(at.rune)+QuoteMinRunes
		for end < message.runes() && from < source.runes() && message.rune(end) == source.rune(from) {
			end, from = end+1, from+1
		}
		first, last := i, end-1
		for first < last && message.rune(first) == " " {
			first++
		}
		for last > first && message.rune(last) == " " {
			last--
		}
		if q := (Quote{Start: int(message.starts[first]), End: message.end(last), ReplyItemID: replies[at.source].ItemID}); !overlaps(quotes, q) {
			quotes = append(quotes, q)
		}
		i = end
	}
	slices.SortFunc(quotes, func(a, b Quote) int { return a.Start - b.Start })
	return quotes
}

func quotedLines(text string, replies []QuoteSource, sources []normalized) []Quote {
	var quotes []Quote
	var block *Quote
	var content []string
	closeBlock := func() {
		if block == nil {
			return
		}
		quoted := normalize(strings.Join(content, "\n")).text
		for i, source := range sources {
			if strings.TrimSpace(quoted) != "" && strings.Contains(source.text, strings.TrimSpace(quoted)) {
				block.ReplyItemID = replies[i].ItemID
				break
			}
		}
		quotes = append(quotes, *block)
		block, content = nil, nil
	}
	for start := 0; start < len(text); {
		end := strings.IndexByte(text[start:], '\n')
		next := len(text)
		if end < 0 {
			end = len(text)
		} else {
			end += start
			next = end + 1
		}
		line := strings.TrimSuffix(text[start:end], "\r")
		rest, ok := strings.CutPrefix(line, "> ")
		if !ok && line == ">" {
			rest, ok = "", true
		}
		if ok {
			if block == nil {
				block = &Quote{Start: start}
			}
			block.End = start + len(line)
			content = append(content, rest)
		} else {
			closeBlock()
		}
		start = next
	}
	closeBlock()
	return quotes
}

func overlaps(quotes []Quote, q Quote) bool {
	for _, other := range quotes {
		if q.Start < other.End && other.Start < q.End {
			return true
		}
	}
	return false
}

func Typed(text string, quotes []Quote) string {
	return render(text, quotes, func(string) string { return "" })
}

func Marked(text string, quotes []Quote) string {
	return render(text, quotes, func(quoted string) string {
		if strings.HasPrefix(quoted, ">") {
			return quoted
		}
		return "> " + strings.ReplaceAll(strings.TrimSpace(quoted), "\n", "\n> ")
	})
}

func render(text string, quotes []Quote, quote func(string) string) string {
	var parts []string
	keep := func(part string) {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	at := 0
	for _, q := range validQuotes(text, quotes) {
		keep(text[at:q.Start])
		keep(quote(text[q.Start:q.End]))
		at = q.End
	}
	keep(text[at:])
	return strings.Join(parts, "\n")
}

func QuotedText(text string, quotes []Quote) []string {
	var out []string
	for _, q := range validQuotes(text, quotes) {
		out = append(out, text[q.Start:q.End])
	}
	return out
}

func validQuotes(text string, quotes []Quote) []Quote {
	var valid []Quote
	at := 0
	for _, q := range quotes {
		if q.Start < at || q.End <= q.Start || q.End > len(text) || !utf8.RuneStart(text[q.Start]) || (q.End < len(text) && !utf8.RuneStart(text[q.End])) {
			continue
		}
		valid = append(valid, q)
		at = q.End
	}
	return valid
}
