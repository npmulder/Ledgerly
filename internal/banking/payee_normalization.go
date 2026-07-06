package banking

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	cardSuffixPattern = regexp.MustCompile(`\b(?:card|visa|mastercard|mc|ending|ends|tail)\s*(?:x{2,}|\*{2,})?\s*\d{4}\b`)
	maskedCardPattern = regexp.MustCompile(`\b(?:x{2,}|\*{2,})\s*\d{4}\b`)
)

// NormalizePayee returns the stable matcher representation shared by
// suggestion engines and payee rules.
func NormalizePayee(payee string) string {
	value := strings.ToLower(strings.TrimSpace(payee))
	value = cardSuffixPattern.ReplaceAllString(value, " ")
	value = maskedCardPattern.ReplaceAllString(value, " ")

	var b strings.Builder
	lastSpace := true
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastSpace = false
		case unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r):
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		default:
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}
