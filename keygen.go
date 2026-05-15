package parchment

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"unicode"
)

var vowels = map[rune]bool{
	'A': true, 'E': true, 'I': true, 'O': true, 'U': true,
}

// DeriveKey produces a short uppercase key (3+ letters) from name that
// does not collide with any key in existing. The algorithm:
//  1. Extract consonant skeleton (first letter + first two consonants)
//  2. On collision, shuffle the last letter through unused source letters
//  3. If exhausted, try A-Z
//  4. If all 26 taken, extend to 4 letters
func DeriveKey(name string, existing map[string]bool) string {
	candidate := ExtractConsonantSkeleton(name)
	if !existing[candidate] {
		return candidate
	}

	base := candidate[:2]
	upper := toAlpha(name)

	for _, ch := range unusedLetters(upper, candidate) {
		alt := base + string(ch)
		if !existing[alt] {
			return alt
		}
	}

	for c := byte('A'); c <= 'Z'; c++ {
		alt := base + string(c)
		if !existing[alt] {
			return alt
		}
	}

	return candidate + "A"
}

// ExtractConsonantSkeleton returns a 3-letter uppercase key derived from name.
// It selects the first letter plus up to two consonants from the remainder.
// If fewer than two consonants exist, vowels backfill. Selected letters are
// emitted in their original positional order so the key reads naturally
// (e.g. "bug" -> BUG, not BGU).
func ExtractConsonantSkeleton(name string) string {
	upper := toAlpha(name)
	if len(upper) == 0 {
		return "XXX"
	}

	runes := []rune(upper)
	selected := make([]bool, len(runes))
	selected[0] = true
	count := 1

	for i := 1; i < len(runes) && count < 3; i++ {
		if !vowels[runes[i]] {
			selected[i] = true
			count++
		}
	}

	for i := 1; i < len(runes) && count < 3; i++ {
		if !selected[i] {
			selected[i] = true
			count++
		}
	}

	var result []rune
	for i, r := range runes {
		if selected[i] {
			result = append(result, r)
		}
	}

	for len(result) < 3 {
		result = append(result, runes[len(runes)-1])
	}

	return string(result[:3])
}

func toAlpha(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// GenerateUUID returns a random UUID v4 string (xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx).
func GenerateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// IsUUIDShaped reports whether s matches the UUID format (8-4-4-4-12 hex with dashes).
func IsUUIDShaped(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return true
}

func unusedLetters(source, used string) []rune {
	usedSet := make(map[rune]bool)
	for _, r := range used {
		usedSet[r] = true
	}
	var result []rune
	seen := make(map[rune]bool)
	for _, r := range source {
		if !usedSet[r] && !seen[r] {
			result = append(result, r)
			seen[r] = true
		}
	}
	return result
}
