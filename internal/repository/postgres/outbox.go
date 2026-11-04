package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/HollowKaeden/pvz-service/internal/events"
	"github.com/HollowKaeden/pvz-service/internal/eventschema"
)

// OutboxRepository хранит события отдельно от Kafka и от жизненного цикла
// ПВЗ: удаление данных демо выполняется явной очисткой outbox.
type OutboxRepository struct{ db *DB }

// NewOutboxRepository создаёт репозиторий outbox.
func NewOutboxRepository(db *DB) *OutboxRepository { return &OutboxRepository{db: db} }

// Add вызывается только в транзакции закрытия приёмки.
func (r *OutboxRepository) Add(ctx context.Context, event eventschema.Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("JSON события: %w", err)
	}
	_, err = r.db.conn(ctx).Exec(ctx, `INSERT INTO outbox (event_id, pvz_id, body) VALUES ($1, $2, $3)`,
		event.ID, event.Payload.PVZID, body)
	if err != nil {
		return fmt.Errorf("insert outbox: %w", err)
	}
	return nil
}

// Next блокирует первую неотправленную запись до конца транзакции relay.
func (r *OutboxRepository) Next(ctx context.Context) (events.Message, bool, error) {
	var m events.Message
	var key string
	err := r.db.conn(ctx).QueryRow(ctx, `
		SELECT id, pvz_id::text, body FROM outbox
		WHERE sent_at IS NULL ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&m.ID, &key, &m.Value)
	if errors.Is(err, pgx.ErrNoRows) {
		return events.Message{}, false, nil
	}
	if err != nil {
		return events.Message{}, false, fmt.Errorf("select outbox: %w", err)
	}
	m.Key = []byte(key)
	return m, true, nil
}

// MarkSent вызывается после подтверждения Kafka, в той же транзакции, что Next.
func (r *OutboxRepository) MarkSent(ctx context.Context, id int64) error {
	_, err := r.db.conn(ctx).Exec(ctx, `UPDATE outbox SET sent_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark outbox sent: %w", err)
	}
	return nil
}

// Pending считает только неотправленные записи по частичному индексу.
func (r *OutboxRepository) Pending(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.conn(ctx).QueryRow(ctx, `SELECT count(*) FROM outbox WHERE sent_at IS NULL`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count outbox: %w", err)
	}
	return n, nil
}

// DeleteSent удаляет не больше 1000 старых записей за один проход.
func (r *OutboxRepository) DeleteSent(ctx context.Context, before time.Time) error {
	_, err := r.db.conn(ctx).Exec(ctx, `DELETE FROM outbox WHERE id IN (
		SELECT id FROM outbox WHERE sent_at < $1 ORDER BY sent_at, id LIMIT 1000
		FOR UPDATE SKIP LOCKED)`, before)
	if err != nil {
		return fmt.Errorf("delete sent outbox: %w", err)
	}
	return nil
}
