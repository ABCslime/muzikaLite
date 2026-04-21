package discogs

import (
	"context"
	"sync"
	"time"
)

// tokenBucket is a simple rate limiter sized for Discogs' 60 req/min
// authenticated quota. One token per second steady-state with a small burst
// allowance keeps the client well under the limit even if the worker pool
// and a user-initiated search (v0.4 PR 3) both compete for budget.
//
// The implementation is deliberately boring: single mutex, `time.Now()` for
// refill, no goroutine. golang.org/x/time/rate would work fine too, but it's
// a transitive dependency we don't otherwise need.
type tokenBucket struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
	now        func() time.Time // swap in tests
}

func newTokenBucket(maxTokens, refillPerSec float64) *tokenBucket {
	return &tokenBucket{
		tokens:     maxTokens,
		maxTokens:  maxTokens,
		refillRate: refillPerSec,
		lastRefill: time.Now(),
		now:        time.Now,
	}
}

// Wait blocks until one token is available, or ctx is cancelled.
// Returns ctx.Err() on cancellation.
func (b *tokenBucket) Wait(ctx context.Context) error {
	for {
		wait, ok := b.tryTake()
		if ok {
			return nil
		}
		// Sleep for `wait`, but honor ctx the whole way.
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// tryTake consumes one token if available. Otherwise returns the duration
// until the next token will be available, and (false).
func (b *tokenBucket) tryTake() (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.refillRate
		if b.tokens > b.maxTokens {
			b.tokens = b.maxTokens
		}
		b.lastRefill = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return 0, true
	}
	missing := 1 - b.tokens
	wait := time.Duration(missing/b.refillRate*float64(time.Second)) + time.Millisecond
	return wait, false
}
