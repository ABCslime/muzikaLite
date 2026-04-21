// Package bus is the in-process event bus that replaces Kafka.
//
// Two delivery modes:
//   - State-change events go through the transactional outbox (see outbox.go).
//   - Request events (DiscoveryIntent, RequestDownload) are Published directly.
//
// Fan-out model: each call to Subscribe[T] returns a fresh channel. Publish[T]
// iterates all subscribers and sends to each. Two consumers on the same event
// type each see every event. See ARCHITECTURE.md §4.
package bus

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"time"
)

// Bus holds per-type subscriber lists. Zero value is not ready; use New.
type Bus struct {
	mu         sync.RWMutex
	subs       map[reflect.Type][]subscriber
	bufferSize int
	log        *slog.Logger
	wg         sync.WaitGroup
	closed     bool
}

type subscriber struct {
	name string
	ch   any // typed as chan T at Subscribe time; asserted at Publish time.
}

// New returns a Bus with the given default buffer size for subscriber channels.
func New(bufferSize int, log *slog.Logger) *Bus {
	if bufferSize < 1 {
		bufferSize = 64
	}
	return &Bus{
		subs:       make(map[reflect.Type][]subscriber),
		bufferSize: bufferSize,
		log:        log,
	}
}

// WaitGroup exposes the internal WaitGroup so callers (RunPool, outbox
// dispatcher) can register their goroutines for graceful shutdown.
func (b *Bus) WaitGroup() *sync.WaitGroup { return &b.wg }

// Close marks the bus closed. No further Publish calls succeed. Existing
// subscriber channels are left open so in-flight work drains; workers exit
// when their context is cancelled, not when the bus closes.
func (b *Bus) Close() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
}

// Wait blocks until every goroutine registered on the bus WaitGroup exits.
func (b *Bus) Wait() { b.wg.Wait() }

// Subscribe registers a new subscriber for events of type T and returns its
// receive-only channel. Channel buffer equals the bus default.
//
// Generic functions live outside the Bus method set because Go doesn't allow
// generic methods on non-generic types.
func Subscribe[T any](b *Bus, name string) <-chan T {
	t := reflect.TypeOf((*T)(nil)).Elem()
	ch := make(chan T, b.bufferSize)
	b.mu.Lock()
	b.subs[t] = append(b.subs[t], subscriber{name: name, ch: ch})
	b.mu.Unlock()
	return ch
}

// PublishOpts controls send behavior for one Publish call.
type PublishOpts struct {
	// If non-zero, the send to each subscriber is bounded by this timeout.
	// On timeout the event is dropped-with-log rather than blocking.
	// State-change events should leave this zero (block forever).
	// Request events should set a small value (e.g. 100ms).
	SendTimeout time.Duration
}

// Publish fans the event out to every subscriber of type T. Returns
// context.Canceled / context.DeadlineExceeded if ctx expires while blocked.
func Publish[T any](ctx context.Context, b *Bus, event T, opts PublishOpts) error {
	t := reflect.TypeOf((*T)(nil)).Elem()

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return ErrClosed
	}
	subs := append([]subscriber(nil), b.subs[t]...)
	b.mu.RUnlock()

	for _, s := range subs {
		ch, ok := s.ch.(chan T)
		if !ok {
			// Should be unreachable given Subscribe's type discipline.
			b.log.Error("bus: subscriber channel type mismatch",
				"event", t.Name(), "subscriber", s.name)
			continue
		}
		if err := sendOne(ctx, ch, event, opts, s.name, t.Name(), b.log); err != nil {
			return err
		}
	}
	return nil
}

func sendOne[T any](
	ctx context.Context,
	ch chan T,
	event T,
	opts PublishOpts,
	subName, evName string,
	log *slog.Logger,
) error {
	if opts.SendTimeout == 0 {
		select {
		case ch <- event:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	timer := time.NewTimer(opts.SendTimeout)
	defer timer.Stop()
	select {
	case ch <- event:
		return nil
	case <-timer.C:
		log.Warn("bus: dropped event (subscriber full)",
			"event", evName, "subscriber", subName, "timeout", opts.SendTimeout)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
