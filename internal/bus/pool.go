package bus

import (
	"context"
	"errors"
	"log/slog"
)

// ErrClosed is returned by Publish after Bus.Close has been called.
var ErrClosed = errors.New("bus: closed")

// RunPool starts `workers` goroutines, each ranging over ch and invoking handler.
// Registers itself with b's WaitGroup so main.go's shutdown path can wait.
// Exits when ctx is cancelled; remaining in-channel events are left behind.
// Handler errors are logged and swallowed (at-least-once semantics from the
// outbox + idempotent handlers mean retry is the dispatcher's job, not ours).
func RunPool[T any](
	ctx context.Context,
	b *Bus,
	name string,
	workers int,
	ch <-chan T,
	handler func(context.Context, T) error,
) {
	if workers < 1 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		b.wg.Add(1)
		go func(workerID int) {
			defer b.wg.Done()
			runWorker(ctx, b.log, name, workerID, ch, handler)
		}(i)
	}
}

func runWorker[T any](
	ctx context.Context,
	log *slog.Logger,
	name string,
	id int,
	ch <-chan T,
	handler func(context.Context, T) error,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := handler(ctx, ev); err != nil {
				log.Error("worker handler failed",
					"pool", name, "worker", id, "err", err)
			}
		}
	}
}
