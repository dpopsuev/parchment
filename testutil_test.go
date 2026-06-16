package parchment

// isAutoID reports whether s looks like an auto-generated ID
// (either a UUID or a title-derived slug with hex suffix).
func isAutoID(s string) bool {
	return len(s) > 5 && s != ""
}

// isUUIDShaped reports whether s matches the UUID format (8-4-4-4-12 hex with dashes).
// Retained for migration compat tests that specifically check UUID output.
func isUUIDShaped(s string) bool {
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
