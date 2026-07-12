package parchment

import "time"

const ExtraKeyClaim = "claim"

// Claim is a lease on an artifact stored in Extra["claim"].
type Claim struct {
	Agent     string    `json:"agent"`
	Session   string    `json:"session,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ClaimFromExtra extracts a Claim from Extra, if present.
func ClaimFromExtra(extra map[string]any) (*Claim, bool) {
	if extra == nil {
		return nil, false
	}
	raw, ok := extra[ExtraKeyClaim]
	if !ok || raw == nil {
		return nil, false
	}
	switch v := raw.(type) {
	case Claim:
		c := v
		return &c, true
	case *Claim:
		if v == nil {
			return nil, false
		}
		c := *v
		return &c, true
	case map[string]any:
		c := Claim{}
		if agent, _ := v["agent"].(string); agent != "" {
			c.Agent = agent
		}
		if session, _ := v["session"].(string); session != "" {
			c.Session = session
		}
		switch exp := v["expires_at"].(type) {
		case time.Time:
			c.ExpiresAt = exp
		case string:
			if t, err := time.Parse(time.RFC3339, exp); err == nil {
				c.ExpiresAt = t
			}
		}
		if c.Agent == "" {
			return nil, false
		}
		return &c, true
	default:
		return nil, false
	}
}

// ClaimActive reports whether the claim is still holding at now.
func ClaimActive(c *Claim, now time.Time) bool {
	if c == nil || c.Agent == "" {
		return false
	}
	if c.ExpiresAt.IsZero() {
		return true
	}
	return now.Before(c.ExpiresAt)
}

// ApplyClaim sets Extra.claim, cloning the map when needed.
func ApplyClaim(extra map[string]any, c Claim) map[string]any {
	out := cloneExtra(extra)
	out[ExtraKeyClaim] = map[string]any{
		"agent":      c.Agent,
		"session":    c.Session,
		"expires_at": c.ExpiresAt.UTC().Format(time.RFC3339),
	}
	return out
}

// ClearClaim removes Extra.claim.
func ClearClaim(extra map[string]any) map[string]any {
	if extra == nil {
		return nil
	}
	out := cloneExtra(extra)
	delete(out, ExtraKeyClaim)
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneExtra(extra map[string]any) map[string]any {
	out := make(map[string]any, len(extra)+1)
	for k, v := range extra {
		out[k] = v
	}
	return out
}
