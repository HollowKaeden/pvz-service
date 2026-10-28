package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// newCommitFailTx — менеджер транзакций, у которого всё внутри транзакции
// проходит, а коммит падает.
func newCommitFailTx(t *testing.T, commitErr error) *mockTxManager {
	t.Helper()
	tx := newMockTxManager(t)
	tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error {
			if err := fn(context.WithValue(ctx, txKey{}, true)); err != nil {
				return err
			}
			return commitErr
		})
	return tx
}

// Все шаги внутри транзакции успешны, но коммит не прошёл: в базе ничего не
// изменилось, и метрики тоже не должны измениться. Мок метрик без ожиданий
// падает на любом вызове.
func TestMetricsNotCountedWhenCommitFails(t *testing.T) {
	errCommit := errors.New("commit: connection reset")

	t.Run("открытие приёмки", func(t *testing.T) {
		pvz := newMockPVZRepository(t)
		receptions := newMockReceptionRepository(t)
		pvz.EXPECT().Lock(inTx, testPVZID).Return(nil)
		receptions.EXPECT().LockOpen(inTx, testPVZID).Return(domain.Reception{}, domain.ErrNoOpenReception)
		receptions.EXPECT().Create(inTx, mock.Anything).Return(nil)
		s := NewReception(newCommitFailTx(t, errCommit), pvz, receptions, newMockMetrics(t))

		_, err := s.Open(context.Background(), testPVZID)

		require.ErrorIs(t, err, errCommit)
	})

	t.Run("закрытие приёмки", func(t *testing.T) {
		receptions := newMockReceptionRepository(t)
		receptions.EXPECT().LockOpen(inTx, testPVZID).Return(openReception, nil)
		receptions.EXPECT().HasProducts(inTx, testReceptionID).Return(true, nil)
		receptions.EXPECT().SetStatus(inTx, testReceptionID, domain.ReceptionClosed).Return(nil)
		s := NewReception(newCommitFailTx(t, errCommit), newMockPVZRepository(t), receptions, newMockMetrics(t))

		_, err := s.Close(context.Background(), testPVZID)

		require.ErrorIs(t, err, errCommit)
	})

	t.Run("добавление товара", func(t *testing.T) {
		receptions := newMockReceptionRepository(t)
		products := newMockProductRepository(t)
		receptions.EXPECT().LockOpen(inTx, testPVZID).Return(openReception, nil)
		products.EXPECT().Create(inTx, mock.Anything).Return(nil)
		s := newTestProduct(t, newCommitFailTx(t, errCommit), newMockPVZRepository(t), receptions, products,
			newMockMetrics(t))

		_, err := s.Add(context.Background(), testPVZID, "обувь")

		require.ErrorIs(t, err, errCommit)
	})
}
