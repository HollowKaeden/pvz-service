// Package events доставляет сохранённые события из outbox в Kafka.
// Kafka-клиент не выходит за границы этого пакета.
package events

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Message — сохранённое сообщение. ID — порядок в outbox, Key — id ПВЗ,
// Value — неизменяемый JSON, включая UUID события для дедупликации.
type Message struct {
	ID    int64
	Key   []byte
	Value []byte
}

// OutboxRepository работает в транзакции relay, переданной через context.
type OutboxRepository interface {
	Next(ctx context.Context) (Message, bool, error)
	MarkSent(ctx context.Context, id int64) error
	Pending(ctx context.Context) (int64, error)
	DeleteSent(ctx context.Context, before time.Time) error
}

// TxManager управляет транзакцией чтения и подтверждения сообщения.
type TxManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

// Publisher ждёт подтверждения записи брокером перед возвратом nil.
type Publisher interface {
	Publish(ctx context.Context, message Message) error
}

// Metrics получает размер очереди, не привязывая relay к Prometheus.
type Metrics interface {
	OutboxPending(n int64)
}

// Relay последовательно отправляет события. Одновременно работает один
// экземпляр; SKIP LOCKED не даёт ждать строки, занятой другой транзакцией.
type Relay struct {
	tx        TxManager
	repo      OutboxRepository
	publisher Publisher
	metrics   Metrics
	log       *slog.Logger
	interval  time.Duration
}

// NewRelay собирает relay. Ошибки зависимостей приводят к повтору, а не к
// остановке HTTP API. В ожидании брокера занято одно соединение пула.
func NewRelay(tx TxManager, repo OutboxRepository, publisher Publisher, metrics Metrics, log *slog.Logger) *Relay {
	return &Relay{tx: tx, repo: repo, publisher: publisher, metrics: metrics, log: log, interval: time.Second}
}

// PublishNext отправляет одно сообщение под блокировкой outbox. При ошибке
// отправки или коммита запись остаётся неотправленной: возможен дубль с тем
// же UUID, потеря события невозможна. Следующее событие ждёт успеха этого.
func (r *Relay) PublishNext(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var found bool
	err := r.tx.Do(ctx, func(ctx context.Context) error {
		message, ok, err := r.repo.Next(ctx)
		if err != nil || !ok {
			return err
		}
		found = true
		if err := r.publisher.Publish(ctx, message); err != nil {
			return fmt.Errorf("публикация outbox %d: %w", message.ID, err)
		}
		return r.repo.MarkSent(ctx, message.ID)
	})
	return found && err == nil, err
}

// Run живёт в общем errgroup процесса до отмены context. Пустая очередь
// опрашивается раз в секунду, ошибки — с паузой до 30 с. Отправленные
// записи хранятся сутки, очистка небольшими порциями не блокирует API.
func (r *Relay) Run(ctx context.Context) error {
	backoff := r.interval
	var sampledAt, cleanedAt time.Time
	for ctx.Err() == nil {
		now := time.Now()
		if now.Sub(sampledAt) >= r.interval {
			r.sample(ctx)
			sampledAt = now
		}
		if now.Sub(cleanedAt) >= time.Minute {
			cleanCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := r.repo.DeleteSent(cleanCtx, now.Add(-24*time.Hour))
			cancel()
			if err != nil && ctx.Err() == nil {
				r.log.WarnContext(ctx, "очистка outbox", slog.Any("error", err))
			}
			cleanedAt = now
		}

		sent, err := r.PublishNext(ctx)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // Отмена общего контекста — штатная остановка, даже если отправка прервалась.
		}
		pause := r.interval
		if err != nil {
			r.log.WarnContext(ctx, "доставка события отложена", slog.Any("error", err), slog.Duration("retry_in", backoff))
			pause = backoff
			backoff = min(2*backoff, 30*time.Second)
		} else {
			backoff = r.interval
			if sent {
				continue
			}
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return nil
}

func (r *Relay) sample(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := r.repo.Pending(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.log.WarnContext(ctx, "размер outbox", slog.Any("error", err))
		}
		return
	}
	r.metrics.OutboxPending(n)
}
