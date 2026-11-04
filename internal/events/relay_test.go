package events

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func relayTx(t *testing.T, commitErr error) *mockTxManager {
	t.Helper()
	tx := newMockTxManager(t)
	tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
		if err := fn(ctx); err != nil {
			return err
		}
		return commitErr
	})
	return tx
}

func TestPublishNext(t *testing.T) {
	errFailure := errors.New("сбой зависимости")
	message := Message{ID: 42, Key: []byte("pvz"), Value: []byte(`{"id":"stable"}`)}
	for _, stage := range []string{"успех", "пусто", "чтение", "публикация", "отметка", "коммит"} {
		t.Run(stage, func(t *testing.T) {
			repo := newMockOutboxRepository(t)
			publisher := newMockPublisher(t)
			var commitErr, nextErr, publishErr, markErr error
			switch stage {
			case "чтение":
				nextErr = errFailure
			case "публикация":
				publishErr = errFailure
			case "отметка":
				markErr = errFailure
			case "коммит":
				commitErr = errFailure
			}
			found := stage != "пусто" && nextErr == nil
			repo.EXPECT().Next(mock.Anything).Return(message, found, nextErr).Once()
			if found {
				publisher.EXPECT().Publish(mock.Anything, message).Return(publishErr).Once()
				if publishErr == nil {
					repo.EXPECT().MarkSent(mock.Anything, message.ID).Return(markErr).Once()
				}
			}
			r := NewRelay(relayTx(t, commitErr), repo, publisher, newMockMetrics(t), slog.New(slog.DiscardHandler))
			sent, err := r.PublishNext(t.Context())
			if stage == "успех" || stage == "пусто" {
				require.NoError(t, err)
				assert.Equal(t, found, sent)
			} else {
				require.ErrorIs(t, err, errFailure)
				assert.False(t, sent)
			}
		})
	}
}

func TestRelayRetriesAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	repo := newMockOutboxRepository(t)
	publisher := newMockPublisher(t)
	metrics := newMockMetrics(t)
	message := Message{ID: 1, Key: []byte("pvz"), Value: []byte("event")}
	repo.EXPECT().Pending(mock.Anything).Return(int64(1), nil)
	repo.EXPECT().DeleteSent(mock.Anything, mock.Anything).Return(nil).Once()
	metrics.EXPECT().OutboxPending(int64(1))
	repo.EXPECT().Next(mock.Anything).Return(message, true, nil).Twice()
	publisher.EXPECT().Publish(mock.Anything, message).Return(errors.New("Kafka выключена")).Once()
	publisher.EXPECT().Publish(mock.Anything, message).Return(nil).Once()
	repo.EXPECT().MarkSent(mock.Anything, message.ID).Run(func(context.Context, int64) { cancel() }).Return(nil).Once()
	r := NewRelay(relayTx(t, nil), repo, publisher, metrics, slog.New(slog.DiscardHandler))
	r.interval = time.Millisecond
	require.NoError(t, r.Run(ctx))
}

func TestKafkaUnavailableAndCancellation(t *testing.T) {
	kafka, err := NewKafka([]string{"127.0.0.1:1"}, "test")
	require.NoError(t, err, "создание клиента не требует брокера")
	defer kafka.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = kafka.Publish(ctx, Message{Key: []byte("pvz"), Value: []byte("event")})
	require.ErrorIs(t, err, context.Canceled)
}
