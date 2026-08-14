package sentence

import (
	"strings"
	"unicode"
)

var abbreviations = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "dr": true, "prof": true,
	"sr": true, "jr": true, "st": true, "vs": true, "etc": true,
	"e.g": true, "i.e": true, "no": true, "fig": true, "vol": true,
	"dept": true, "est": true, "approx": true,
}

func isTerminal(r rune) bool {
	return r == '.' || r == '!' || r == '?'
}

func isClosing(r rune) bool {
	switch r {
	case '"', '\'', '”', '’', ')', ']', '}', '»', '›':
		return true
	default:
		return false
	}
}

// Split breaks an English paragraph into sentences. It is deliberately
// conservative: it only cuts at terminal punctuation that is followed by an
// uppercase letter / digit (or the end of text) and is not part of a known
// abbreviation. A hard newline always starts a new sentence.
func Split(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var out []string
	var current []rune
	runes := []rune(text)
	flush := func() {
		s := strings.TrimSpace(string(current))
		if s != "" {
			out = append(out, s)
		}
		current = current[:0]
	}

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\n' {
			flush()
			continue
		}
		current = append(current, r)
		if !isTerminal(r) {
			continue
		}

		// Ellipsis "...": treat the group as one terminal only at its end.
		if r == '.' && i+1 < len(runes) && runes[i+1] == '.' {
			continue
		}

		// Look ahead past whitespace and closing quotes.
		j := i + 1
		for j < len(runes) && (unicode.IsSpace(runes[j]) || isClosing(runes[j])) {
			j++
		}
		if j >= len(runes) {
			flush()
			continue
		}
		next := runes[j]
		if !unicode.IsUpper(next) && !unicode.IsDigit(next) {
			continue
		}
		// Decimal numbers such as "3.14" should not split.
		if r == '.' && i > 0 && unicode.IsDigit(runes[i-1]) && unicode.IsDigit(next) {
			continue
		}

		// Skip abbreviations such as "Mr. Holmes" or "St. Petersburg".
		wordEnd := i
		for wordEnd > 0 && !unicode.IsLetter(runes[wordEnd-1]) {
			wordEnd--
		}
		wordStart := wordEnd
		for wordStart > 0 && unicode.IsLetter(runes[wordStart-1]) {
			wordStart--
		}
		if abbreviations[strings.ToLower(string(runes[wordStart:wordEnd]))] {
			continue
		}

		flush()
	}

	flush()
	if len(out) == 0 {
		return []string{strings.TrimSpace(text)}
	}
	return out
}
