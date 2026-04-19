package ssh

import "time"

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
)

// Backoff implements exponential reconnect backoff capped at 30 seconds.
type Backoff struct {
	current time.Duration
}

// NewBackoff creates a backoff starting at 1 second.
func NewBackoff() *Backoff {
	return &Backoff{current: initialBackoff}
}

// Next returns the current backoff and advances the sequence.
func (b *Backoff) Next() time.Duration {
	delay := b.current
	b.current *= 2
	if b.current > maxBackoff {
		b.current = maxBackoff
	}
	return delay
}

// Reset returns the backoff to its initial value.
func (b *Backoff) Reset() {
	b.current = initialBackoff
}
