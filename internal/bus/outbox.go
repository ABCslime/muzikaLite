package bus

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// OutboxDispatcher polls the outbox table and republishes events to the bus.
//
// Wake-up pattern:
//   - Dispatcher loop selects on either the wake channel or a 500ms ticker.
//   - Publishers (after committing a row to outbox) do a non-blocking send:
//       select { case wake <- struct{}{}: default: }
//   - The wake channel is buffered size 1 so a single pending notification is
//     retained across multiple commits without the publisher ever blocking.
//     If 10 publishers commit in rapid succession, the dispatcher will see
//     one wake and drain all 10 rows in the same pass.
//
// This lets the dispatcher react within microseconds of a commit while still
// having the ticker as a safety net for rows inserted through other paths
// (migrations, manual inserts, crash recovery).
type OutboxDispatcher struct {
	db     *sql.DB
	bus    *Bus
	log    *slog.Logger
	wake   chan struct{}
	stop   chan struct{}
	stopped chan struct{}
}

// StartOutboxDispatcher starts the dispatcher goroutine. Stop with Stop().
func StartOutboxDispatcher(ctx context.Context, db *sql.DB, bus *Bus, log *slog.Logger) *OutboxDispatcher {
	d := &OutboxDispatcher{
		db:      db,
		bus:     bus,
		log:     log,
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	bus.wg.Add(1)
	go func() {
		defer bus.wg.Done()
		defer close(d.stopped)
		d.run(ctx)
	}()
	return d
}

// Wake notifies the dispatcher that rows are available. Non-blocking.
// Safe to call from any goroutine after committing to outbox.
func (d *OutboxDispatcher) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Stop signals the dispatcher to exit and waits for the goroutine to return.
func (d *OutboxDispatcher) Stop() {
	close(d.stop)
	<-d.stopped
}

func (d *OutboxDispatcher) run(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stop:
			return
		case <-d.wake:
		case <-ticker.C:
		}
		if err := d.drainOnce(ctx); err != nil {
			d.log.Error("outbox: drain pass failed", "err", err)
		}
	}
}

// drainOnce processes up to 64 rows. Returns after one empty SELECT.
func (d *OutboxDispatcher) drainOnce(ctx context.Context) error {
	for {
		rows, err := d.db.QueryContext(ctx,
			`SELECT id, event_type, payload FROM outbox ORDER BY id LIMIT 64`)
		if err != nil {
			return fmt.Errorf("select: %w", err)
		}
		var batch []outboxRow
		for rows.Next() {
			var r outboxRow
			if err := rows.Scan(&r.id, &r.eventType, &r.payload); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan: %w", err)
			}
			batch = append(batch, r)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
		if len(batch) == 0 {
			return nil
		}
		for _, r := range batch {
			if err := d.dispatchByType(ctx, r.eventType, r.payload); err != nil {
				d.log.Error("outbox: dispatch failed; leaving for retry",
					"id", r.id, "type", r.eventType, "err", err)
				// Leave the row; next pass will retry.
				return nil
			}
			if _, err := d.db.ExecContext(ctx, `DELETE FROM outbox WHERE id = ?`, r.id); err != nil {
				return fmt.Errorf("delete id=%d: %w", r.id, err)
			}
		}
	}
}

type outboxRow struct {
	id        int64
	eventType string
	payload   []byte
}

// dispatchByType deserializes and Publishes. Keep this switch in sync with events.go.
func (d *OutboxDispatcher) dispatchByType(ctx context.Context, evType string, payload []byte) error {
	switch evType {
	case TypeUserCreated:
		return unmarshalAndPublish[UserCreated](ctx, d.bus, payload)
	case TypeUserDeleted:
		return unmarshalAndPublish[UserDeleted](ctx, d.bus, payload)
	case TypeLikedSong:
		return unmarshalAndPublish[LikedSong](ctx, d.bus, payload)
	case TypeUnlikedSong:
		return unmarshalAndPublish[UnlikedSong](ctx, d.bus, payload)
	case TypeLoadedSong:
		return unmarshalAndPublish[LoadedSong](ctx, d.bus, payload)
	default:
		return fmt.Errorf("unknown event type: %s", evType)
	}
}

func unmarshalAndPublish[T any](ctx context.Context, b *Bus, payload []byte) error {
	var ev T
	if err := json.Unmarshal(payload, &ev); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	return Publish(ctx, b, ev, PublishOpts{})
}

// InsertOutboxTx writes one outbox row inside a caller-owned transaction.
// Callers use this to atomically pair a state mutation with its event.
// After tx.Commit() succeeds, call OutboxDispatcher.Wake() to prod the dispatcher.
func InsertOutboxTx(ctx context.Context, tx *sql.Tx, evType string, event any) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", evType, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox (event_type, payload) VALUES (?, ?)`,
		evType, payload,
	); err != nil {
		return fmt.Errorf("insert outbox %s: %w", evType, err)
	}
	return nil
}
