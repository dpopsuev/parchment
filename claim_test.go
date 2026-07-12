package parchment

import (
	"testing"
	"time"
)

func TestClaimFromExtra_RoundTrip(t *testing.T) {
	c := Claim{Agent: "a1", Session: "s1", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	extra := ApplyClaim(nil, c)
	got, ok := ClaimFromExtra(extra)
	if !ok || got.Agent != "a1" || got.Session != "s1" {
		t.Fatalf("got %#v ok=%v", got, ok)
	}
	if !ClaimActive(got, time.Now()) {
		t.Fatal("expected active")
	}
	extra = ClearClaim(extra)
	if _, ok := ClaimFromExtra(extra); ok {
		t.Fatal("expected cleared")
	}
}

func TestClaimActive_Expired(t *testing.T) {
	c := &Claim{Agent: "a1", ExpiresAt: time.Now().Add(-time.Minute)}
	if ClaimActive(c, time.Now()) {
		t.Fatal("expected expired")
	}
}
