package parchment

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode"
)

// GenerateUUID returns a random UUID v4 string.
func GenerateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

var slugMultiDash = regexp.MustCompile(`-{2,}`)

// Slugify converts a title into a URL-friendly, human-readable ID.
// "Faceted Classification (PMEST)" → "faceted-classification-pmest-a1b2"
// Appends a 4-char hex suffix for uniqueness.
func Slugify(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteByte('-')
		}
	}
	s := strings.Trim(slugMultiDash.ReplaceAllString(b.String(), "-"), "-")

	const maxSlug = 60
	if len(s) > maxSlug {
		s = s[:maxSlug]
		if last := strings.LastIndexByte(s, '-'); last > maxSlug/2 {
			s = s[:last]
		}
	}

	suffix := make([]byte, 2)
	_, _ = rand.Read(suffix)
	return s + "-" + hex.EncodeToString(suffix)
}
