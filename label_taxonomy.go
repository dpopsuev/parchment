package parchment

import "strings"

// ExpandLabels returns each label plus all dot-separated ancestor prefixes.
// Labels containing ':' are atomic — "source:github.com" does not expand.
func ExpandLabels(labels []string) []string {
	seen := make(map[string]struct{}, len(labels)*2)
	for _, label := range labels {
		if label == "" {
			continue
		}
		seen[label] = struct{}{}
		// Labels containing ':' are atomic (namespace:value format — e.g. source:github.com).
		// Only pure dot-notation labels (no colon) encode hierarchy.
		if strings.ContainsRune(label, ':') {
			continue
		}
		parts := strings.Split(label, ".")
		for depth := len(parts) - 1; depth >= 1; depth-- {
			seen[strings.Join(parts[:depth], ".")] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	return out
}
