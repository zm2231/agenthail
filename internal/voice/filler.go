package voice

import (
	"strings"
	"unicode"
)

// fillerPhrases are the utterances voice keeps instead of delivering to the
// selected session. The list is closed and conservative: a word that can
// answer a session's question, such as yes, no, okay or sure, is never on it,
// so a held utterance can only be one the session had no use for.
var fillerPhrases = [][]string{
	{"um"}, {"umm"}, {"uh"}, {"uhm"}, {"er"}, {"hmm"}, {"mm"}, {"mhm"},
	{"thanks"}, {"thank", "you"}, {"cool"}, {"nice"}, {"got", "it"},
}

const maxFillerWords = 4

func voiceOnly(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) && r != '\''
	})
	if len(words) == 0 || len(words) > maxFillerWords {
		return false
	}
	for _, word := range words {
		for _, r := range word {
			if !unicode.IsLetter(r) && r != '\'' {
				return false
			}
		}
	}
	for len(words) > 0 {
		matched := false
		for _, phrase := range fillerPhrases {
			if len(phrase) <= len(words) && equalWords(words[:len(phrase)], phrase) {
				words, matched = words[len(phrase):], true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func equalWords(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
