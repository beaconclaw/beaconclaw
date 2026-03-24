package ws

import (
	"testing"
	"time"
)

func TestBackoffExponential(t *testing.T) {
	b := NewBackoff()

	first := b.Next()
	second := b.Next()
	third := b.Next()

	// Each should be roughly 2x the previous (with jitter)
	if second < first/2 {
		t.Errorf("second backoff (%s) should be >= first/2 (%s)", second, first/2)
	}
	if third < second/2 {
		t.Errorf("third backoff (%s) should be >= second/2 (%s)", third, second/2)
	}
}

func TestBackoffMax(t *testing.T) {
	b := NewBackoff()

	// Run enough iterations to hit the max
	var last time.Duration
	for i := 0; i < 20; i++ {
		last = b.Next()
	}

	// With jitter, max should be around 60s +/- 30%
	upper := time.Duration(float64(maxBackoff) * 1.4)
	if last > upper {
		t.Errorf("backoff %s exceeded upper bound %s", last, upper)
	}
}

func TestBackoffReset(t *testing.T) {
	b := NewBackoff()

	b.Next()
	b.Next()
	b.Next()

	if b.Attempt() != 3 {
		t.Fatalf("expected attempt 3, got %d", b.Attempt())
	}

	b.Reset()
	if b.Attempt() != 0 {
		t.Fatalf("expected attempt 0 after reset, got %d", b.Attempt())
	}
}
