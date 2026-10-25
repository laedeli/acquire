package release

import (
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// MatchKey is a title, or the words of a release name, as the two are
// compared: letters and digits in lower case without their accents, an
// apostrophe dropped ("Keeper's" is "keepers", as scene names write it), "&" read
// as "and", and anything else one space between words. "Example Saga:
// Seven Harbours", "Example.Saga.Seven.Harbours" and "Example Saga - Seven
// Harbours" have one key.
func MatchKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	if folded, _, err := transform.String(accents, s); err == nil {
		s = folded
	}
	s = ligatures.Replace(s)
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case r == '\'' || r == '’' || r == '`' || r == '´':
			continue
		case r == '&':
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString("and")
			space = true
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		default:
			space = true
		}
	}
	return b.String()
}

// accents takes the accents off letters: é is e, ü is u.
var accents = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// ligatures are the letters no decomposition takes apart.
var ligatures = strings.NewReplacer("ß", "ss", "æ", "ae", "œ", "oe", "ø", "o", "ł", "l", "đ", "d", "þ", "th")

// YearOf reads a word of a name's key as a year a release names: 1900 to
// 2099. 0 when it is none.
func YearOf(word string) int {
	if len(word) != 4 || (!strings.HasPrefix(word, "19") && !strings.HasPrefix(word, "20")) {
		return 0
	}
	y, err := strconv.Atoi(word)
	if err != nil {
		return 0
	}
	return y
}
