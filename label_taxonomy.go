package parchment

import (
	"context"
	"log/slog"
)

// defaultLabelParents is the seed taxonomy: child → parent.
// Authors write semantic labels (lang:go, domain:auth). The taxonomy lets
// artifact(list, labels=[lang]) return rules labeled lang:go, lang:ts, etc.
// Extend by calling PutLabelParent — no code change required.
var defaultLabelParents = [][2]string{
	{"lang:go", "lang"},
	{"lang:ts", "lang"},
	{"lang:js", "lang"},
	{"lang:py", "lang"},
	{"lang:rust", "lang"},
	{"lang:proto", "lang"},
	{"lang:java", "lang"},
	{"lang:c", "lang"},
	{"lang:cpp", "lang"},
	{"lang:ruby", "lang"},
	{"lang:swift", "lang"},
	{"lang:kotlin", "lang"},
	{"lang:go-test", "lang:go"},
	{"lang:go-test", "testing"},
}

// SeedLabelTaxonomy writes the default label parent relationships.
// Idempotent — uses INSERT OR IGNORE.
func SeedLabelTaxonomy(ctx context.Context, s Store) {
	for _, pair := range defaultLabelParents {
		if err := s.PutLabelParent(ctx, pair[0], pair[1]); err != nil {
			slog.WarnContext(ctx, "seed label taxonomy: put failed",
				slog.String(LogKeyFrom, pair[0]),
				slog.String(LogKeyTo, pair[1]),
				slog.Any(LogKeyError, err))
		}
	}
}
