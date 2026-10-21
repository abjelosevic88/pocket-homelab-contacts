package vcard

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// cyrillic maps Serbian/Macedonian/Russian Cyrillic to Latin (Serbian
// Gaj's alphabet where it differs), so "Јелена" is found by "jelena".
var cyrillic = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'ђ': "đ", 'е': "e", 'ж': "ž", 'з': "z", 'и': "i",
	'ј': "j", 'к': "k", 'л': "l", 'љ': "lj", 'м': "m", 'н': "n", 'њ': "nj", 'о': "o", 'п': "p", 'р': "r",
	'с': "s", 'т': "t", 'ћ': "ć", 'у': "u", 'ф': "f", 'х': "h", 'ц': "c", 'ч': "č", 'џ': "dž", 'ш': "š",
	'ѓ': "gj", 'ќ': "kj", 'ѕ': "dz", 'й': "j", 'ы': "y", 'э': "e", 'ю': "ju", 'я': "ja", 'ё': "jo",
	'щ': "šč", 'ъ': "", 'ь': "", 'і': "i", 'ї': "ji", 'є': "je", 'ґ': "g",
}

// Latin transliterates Cyrillic letters (case kept) and leaves the rest.
func Latin(s string) string {
	if !hasCyrillic(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		l := unicode.ToLower(r)
		if t, ok := cyrillic[l]; ok {
			if l != r && t != "" {
				rs := []rune(t)
				t = string(unicode.ToUpper(rs[0])) + string(rs[1:])
			}
			b.WriteString(t)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

var stripMarks = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// Fold is the comparison form of a name: Latin letters, lower case, no
// diacritics, đ as d, single spaces ("Đorđe Ćosić" → "dorde cosic").
func Fold(s string) string {
	s = strings.ToLower(Latin(s))
	s = strings.NewReplacer("đ", "d", "ł", "l", "ø", "o", "ß", "ss", "æ", "ae", "œ", "oe").Replace(s)
	if out, _, err := transform.String(stripMarks, s); err == nil {
		s = out
	}
	return strings.Join(strings.Fields(s), " ")
}

// SearchForms returns the extra spellings a name should be found by:
// the Latin transliteration and the "dj" spelling of đ.
func SearchForms(s string) string {
	forms := []string{s}
	if l := Latin(s); l != s {
		forms = append(forms, l)
	}
	low := strings.ToLower(Latin(s))
	if strings.Contains(low, "đ") {
		forms = append(forms, strings.ReplaceAll(low, "đ", "dj"))
	}
	return strings.Join(forms, " ")
}

// Digits keeps the digits of a phone number, with a leading + when it had one.
func Digits(s string) string {
	var b strings.Builder
	for i, r := range strings.TrimSpace(s) {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if r == '+' && i == 0 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// PhoneKey is the part of a number that identifies it across formats:
// the last 8 digits (enough to match +387 65 123 456 with 065/123-456).
func PhoneKey(s string) string {
	d := strings.TrimPrefix(Digits(s), "+")
	if len(d) > 8 {
		return d[len(d)-8:]
	}
	return d
}
