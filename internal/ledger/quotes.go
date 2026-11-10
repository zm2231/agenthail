package ledger

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	QuoteMinRunes  = 40
	QuoteReplies   = 5
	quoteSourceMax = 64 << 10
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
	starts     []int
	ends       []int
	runeOffset []int
}

func normalize(text string) normalized {
	var b strings.Builder
	var n normalized
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		start := i
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
		n.runeOffset = append(n.runeOffset, b.Len())
		n.starts, n.ends = append(n.starts, start), append(n.ends, i)
		b.WriteRune(r)
	}
	n.runeOffset = append(n.runeOffset, b.Len())
	n.text = b.String()
	return n
}

func (n normalized) window(i int) string { return n.text[n.runeOffset[i]:n.runeOffset[i+QuoteMinRunes]] }

// FindQuotes returns the spans of text that are quoted rather than typed: runs of lines starting with "> ",
// and stretches of at least QuoteMinRunes runes that equal, whitespace aside, a stretch of one of the replies.
// Replies come newest first; a span names the first reply it matches.
func FindQuotes(text string, replies []QuoteSource) []Quote {
	sources := make([]normalized, len(replies))
	windows := map[string]string{}
	for i, reply := range replies {
		body := reply.Text
		if len(body) > quoteSourceMax {
			body = body[len(body)-quoteSourceMax:]
			for len(body) > 0 && !utf8.RuneStart(body[0]) {
				body = body[1:]
			}
		}
		sources[i] = normalize(body)
		for w := 0; w+QuoteMinRunes <= len(sources[i].starts); w++ {
			if key := sources[i].window(w); windows[key] == "" {
				windows[key] = reply.ItemID
			}
		}
	}
	quotes := quotedLines(text, replies, sources)
	message := normalize(text)
	var run *Quote
	runEnd := 0
	flush := func() {
		if run != nil && !overlaps(quotes, *run) {
			quotes = append(quotes, *run)
		}
		run = nil
	}
	for i := 0; i+QuoteMinRunes <= len(message.starts); i++ {
		id, ok := windows[message.window(i)]
		if !ok {
			continue
		}
		first, last := i, i+QuoteMinRunes-1
		for first < last && message.text[message.runeOffset[first]] == ' ' {
			first++
		}
		for last > first && message.text[message.runeOffset[last]] == ' ' {
			last--
		}
		if run != nil && i <= runEnd {
			run.End = message.ends[last]
		} else {
			flush()
			run = &Quote{Start: message.starts[first], End: message.ends[last], ReplyItemID: id}
		}
		runEnd = i + QuoteMinRunes
	}
	flush()
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
		if rest, ok := strings.CutPrefix(line, "> "); ok || line == ">" {
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
