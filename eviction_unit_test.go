package parchment

import (
	"math"
	"testing"
	"time"
)

// --- statusToQuality ---

func TestEvictionQuality_ViaProtocol(t *testing.T) {
	p := New(NewMemoryStore(), nil, []string{"test"}, nil, ProtocolConfig{})
	cases := map[string]struct {
		status string
		min    float64
	}{
		"evergreen": {"note.evergreen", 0.9},
		"active":    {"work.active", 0.4},
		"fleeting":  {"note.fleeting", 0.1},
	}
	for name, tc := range cases {
		q := p.EvictionQuality(tc.status)
		if q < tc.min {
			t.Errorf("%s: want quality >= %.1f, got %.2f", name, tc.min, q)
		}
	}
}

// --- computeAccessHeat ---

func TestAccessHeatDecay_RecentIsHot(t *testing.T) {
	heat := computeAccessHeat(10, time.Now())
	if heat < 0.9 {
		t.Errorf("recent high-count access: want >= 0.9, got %.2f", heat)
	}
}

func TestAccessHeatDecay_OldIsZero(t *testing.T) {
	heat := computeAccessHeat(100, time.Now().Add(-200*24*time.Hour))
	if heat > 0.1 {
		t.Errorf("old access: want <= 0.1, got %.2f", heat)
	}
}

func TestAccessHeatDecay_NeverAccessed_IsZero(t *testing.T) {
	heat := computeAccessHeat(0, time.Time{})
	if heat != 0 {
		t.Errorf("never accessed: want 0, got %.2f", heat)
	}
}

// --- computeRecency ---

func TestComputeRecency_UpdatedNow_IsOne(t *testing.T) {
	r := computeRecency(time.Now(), 90)
	if math.Abs(float64(r)-1.0) > 0.05 {
		t.Errorf("updated now: want ≈1.0, got %.2f", r)
	}
}

func TestComputeRecency_UpdatedLongAgo_IsNearZero(t *testing.T) {
	r := computeRecency(time.Now().Add(-365*24*time.Hour), 90)
	if r > 0.1 {
		t.Errorf("updated 1y ago: want ≈0.0, got %.2f", r)
	}
}
