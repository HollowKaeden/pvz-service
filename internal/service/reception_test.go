package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// txKey помечает context, который передан внутрь транзакции.
type txKey struct{}

// inTx — matcher для mockery: вызов репозитория сделан внутри транзакции.
var inTx = mock.MatchedBy(func(ctx context.Context) bool {
	return ctx.Value(txKey{}) != nil
})

// newTestTx — менеджер транзакций, который просто вызывает fn с помеченным
// context. Если txErr не nil, транзакция «не открывается» и fn не вызывается.
func newTestTx(t *testing.T, txErr error) *mockTxManager {
	t.Helper()
	tx := newMockTxManager(t)
	tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error {
			if txErr != nil {
				return txErr
			}
			return fn(context.WithValue(ctx, txKey{}, true))
		})
	return tx
}

var (
	testPVZID       = uuid.MustParse("0192f1a4-7b3c-7d2e-8f00-000000000001")
	testReceptionID = uuid.MustParse("0192f1a4-7b3c-7d2e-8f00-0000000000a1")
)

func TestReceptionOpen(t *testing.T) {
	// Время с наносекундами и не в UTC: сервис должен привести его к виду,
	// в котором оно хранится в Postgres.
	now := time.Date(2026, 10, 24, 15, 4, 5, 123456789, time.FixedZone("MSK", 3*60*60))
	want := domain.Reception{
		ID:       testReceptionID,
		PVZID:    testPVZID,
		DateTime: time.Date(2026, 10, 24, 12, 4, 5, 123456000, time.UTC),
		Status:   domain.ReceptionInProgress,
	}
	open := domain.Reception{ID: uuid.New(), PVZID: testPVZID, Status: domain.ReceptionInProgress}
	errDB := errors.New("connection reset")

	tests := []struct {
		name string
		// Ответы репозиториев по порядку вызова. Шаг, до которого сервис не
		// должен дойти, не задаётся: мок упадёт на неожиданном вызове.
		txErr      error
		lockErr    error
		callsFind  bool
		found      bool
		findErr    error
		callsSave  bool
		saveErr    error
		wantErr    error
		wantErrMsg string
	}{
		{
			name:      "приёмка открыта",
			callsFind: true, findErr: domain.ErrNoOpenReception,
			callsSave: true,
		},
		{
			name:    "ПВЗ не найден",
			lockErr: domain.ErrPVZNotFound,
			wantErr: domain.ErrPVZNotFound, wantErrMsg: "ПВЗ не найден",
		},
		{
			name:      "уже есть открытая приёмка",
			callsFind: true, found: true,
			wantErr: domain.ErrReceptionAlreadyOpen, wantErrMsg: "на ПВЗ уже есть незакрытая приёмка",
		},
		{
			// Проверку в сервисе обошли, и сработал уникальный индекс в БД.
			name:      "открытую приёмку поймал индекс",
			callsFind: true, findErr: domain.ErrNoOpenReception,
			callsSave: true, saveErr: domain.ErrReceptionAlreadyOpen,
			wantErr: domain.ErrReceptionAlreadyOpen, wantErrMsg: "на ПВЗ уже есть незакрытая приёмка",
		},
		{name: "транзакция не открылась", txErr: errDB, wantErr: errDB},
		{name: "ошибка БД при блокировке ПВЗ", lockErr: errDB, wantErr: errDB},
		{
			name:      "ошибка БД при поиске открытой приёмки",
			callsFind: true, findErr: errDB,
			wantErr: errDB,
		},
		{
			name:      "ошибка БД при создании",
			callsFind: true, findErr: domain.ErrNoOpenReception,
			callsSave: true, saveErr: errDB,
			wantErr: errDB,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pvz := newMockPVZRepository(t)
			receptions := newMockReceptionRepository(t)
			if tt.txErr == nil {
				pvz.EXPECT().Lock(inTx, testPVZID).Return(tt.lockErr)
			}
			if tt.callsFind {
				var found domain.Reception
				if tt.found {
					found = open
				}
				receptions.EXPECT().LockOpen(inTx, testPVZID).Return(found, tt.findErr)
			}
			if tt.callsSave {
				receptions.EXPECT().Create(inTx, want).Return(tt.saveErr)
			}
			s := NewReception(newTestTx(t, tt.txErr), pvz, receptions)
			s.now = func() time.Time { return now }
			s.newID = func() (uuid.UUID, error) { return testReceptionID, nil }

			got, err := s.Open(context.Background(), testPVZID)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				if tt.wantErrMsg != "" {
					// Текст ошибки бизнес-правила видит клиент: без технических префиксов.
					assert.EqualError(t, err, tt.wantErrMsg)
				}
				assert.Zero(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

func TestReceptionClose(t *testing.T) {
	open := domain.Reception{
		ID:       testReceptionID,
		PVZID:    testPVZID,
		DateTime: time.Date(2026, 10, 24, 12, 4, 5, 123456000, time.UTC),
		Status:   domain.ReceptionInProgress,
	}
	closed := open
	closed.Status = domain.ReceptionClosed
	errDB := errors.New("connection reset")

	tests := []struct {
		name        string
		txErr       error
		findErr     error
		callsExists bool
		exists      bool
		existsErr   error
		callsSet    bool
		setErr      error
		wantErr     error
		wantErrMsg  string
	}{
		{name: "приёмка закрыта", callsSet: true},
		{
			// Сюда же попадает повторное закрытие: закрытая приёмка не открытая.
			name:    "нет открытой приёмки",
			findErr: domain.ErrNoOpenReception, callsExists: true, exists: true,
			wantErr: domain.ErrNoOpenReception, wantErrMsg: "на ПВЗ нет открытой приёмки",
		},
		{
			name:    "ПВЗ не найден",
			findErr: domain.ErrNoOpenReception, callsExists: true,
			wantErr: domain.ErrPVZNotFound, wantErrMsg: "ПВЗ не найден",
		},
		{
			name:    "ошибка БД при проверке ПВЗ",
			findErr: domain.ErrNoOpenReception, callsExists: true, existsErr: errDB,
			wantErr: errDB,
		},
		{name: "транзакция не открылась", txErr: errDB, wantErr: errDB},
		{name: "ошибка БД при поиске открытой приёмки", findErr: errDB, wantErr: errDB},
		{name: "ошибка БД при смене статуса", callsSet: true, setErr: errDB, wantErr: errDB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receptions := newMockReceptionRepository(t)
			if tt.txErr == nil {
				var found domain.Reception
				if tt.findErr == nil {
					found = open
				}
				receptions.EXPECT().LockOpen(inTx, testPVZID).Return(found, tt.findErr)
			}
			pvz := newMockPVZRepository(t)
			if tt.callsExists {
				pvz.EXPECT().Exists(inTx, testPVZID).Return(tt.exists, tt.existsErr)
			}
			if tt.callsSet {
				receptions.EXPECT().SetStatus(inTx, testReceptionID, domain.ReceptionClosed).Return(tt.setErr)
			}
			s := NewReception(newTestTx(t, tt.txErr), pvz, receptions)

			got, err := s.Close(context.Background(), testPVZID)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				if tt.wantErrMsg != "" {
					assert.EqualError(t, err, tt.wantErrMsg)
				}
				assert.Zero(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, closed, got)
		})
	}
}
