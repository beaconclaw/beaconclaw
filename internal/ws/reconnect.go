package ws

import (
	"math"
	"math/rand/v2"
	"time"
)

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 60 * time.Second
	jitterFraction = 0.3
)

// Backoff calculates exponential backoff with jitter for reconnection attempts.
type Backoff struct {
	attempt int
}

func NewBackoff() *Backoff {
	return &Backoff{}
}

// Next returns the duration to wait before the next reconnection attempt.
func (b *Backoff) Next() time.Duration {
	base := float64(initialBackoff) * math.Pow(2, float64(b.attempt))
	if base > float64(maxBackoff) {
		base = float64(maxBackoff)
	}

	// Add jitter: +-30%
	jitter := base * jitterFraction * (2*rand.Float64() - 1)
	d := time.Duration(base + jitter)

	b.attempt++
	return d
}

// Reset resets the backoff counter after a successful connection.
func (b *Backoff) Reset() {
	b.attempt = 0
}

// Attempt returns the current attempt number.
func (b *Backoff) Attempt() int {
	return b.attempt
}
