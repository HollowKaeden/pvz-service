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

var testProductTypes = []string{"электроника", "одежда", "обувь"}

// newTestProduct создаёт сервис товаров со справочником типов из задания.
func newTestProduct(
	t *testing.T, tx TxManager, pvz PVZRepository, receptions ReceptionRepository, products ProductRepository,
	metrics Metrics,
) *Product {
	t.Helper()
	types := newMockProductTypeRepository(t)
	types.EXPECT().List(mock.Anything).Return(testProductTypes, nil)

	s, err := NewProduct(context.Background(), tx, pvz, receptions, products, types, metrics)
	require.NoError(t, err)
	return s
}

// openReception — открытая приёмка, в которую тесты добавляют товары.
var openReception = domain.Reception{
	ID:       testReceptionID,
	PVZID:    testPVZID,
	DateTime: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
	Status:   domain.ReceptionInProgress,
}

// noReception описывает, чем закончился поиск открытой приёмки, если её нет:
// у ПВЗ просто нет открытой приёмки или нет самого ПВЗ.
type noReception struct {
	pvzExists bool
}

// expectLockOpen задаёт ответ на поиск открытой приёмки: найдена openReception,
// не найдена (none != nil) или ошибка БД (err != nil).
func expectLockOpen(
	receptions *mockReceptionRepository, pvz *mockPVZRepository, none *noReception, err error,
) {
	switch {
	case err != nil:
		receptions.EXPECT().LockOpen(inTx, testPVZID).Return(domain.Reception{}, err)
	case none != nil:
		receptions.EXPECT().LockOpen(inTx, testPVZID).Return(domain.Reception{}, domain.ErrNoOpenReception)
		pvz.EXPECT().Exists(inTx, testPVZID).Return(none.pvzExists, nil)
	default:
		receptions.EXPECT().LockOpen(inTx, testPVZID).Return(openReception, nil)
	}
}

func TestProductAdd(t *testing.T) {
	productID := uuid.MustParse("0192f1a4-7b3c-7d2e-8f00-0000000000b1")
	// Время с наносекундами и не в UTC: сервис должен привести его к виду,
	// в котором оно хранится в Postgres.
	now := time.Date(2026, 10, 24, 15, 4, 5, 123456789, time.FixedZone("MSK", 3*60*60))
	want := domain.Product{
		ID:          productID,
		ReceptionID: testReceptionID,
		DateTime:    time.Date(2026, 10, 24, 12, 4, 5, 123456000, time.UTC),
		Type:        "обувь",
	}
	errDB := errors.New("connection reset")

	tests := []struct {
		name        string
		productType string
		// Ответы по порядку вызова. Шаг, до которого сервис не должен дойти,
		// не задаётся: мок упадёт на неожиданном вызове.
		noTx       bool // до транзакции дело не доходит
		none       *noReception
		lockErr    error
		callsSave  bool
		saveErr    error
		wantErr    error
		wantErrMsg string
	}{
		{name: "товар добавлен", productType: "обувь", callsSave: true},
		{
			name: "тип не из справочника", productType: "мебель", noTx: true,
			wantErr:    domain.ErrProductTypeNotAllowed,
			wantErrMsg: `недопустимый тип товара: "мебель" (доступны: электроника, одежда, обувь)`,
		},
		{
			name: "регистр важен", productType: "Обувь", noTx: true,
			wantErr: domain.ErrProductTypeNotAllowed,
		},
		{
			name: "нет открытой приёмки", productType: "обувь", none: &noReception{pvzExists: true},
			wantErr: domain.ErrNoOpenReception, wantErrMsg: "на ПВЗ нет открытой приёмки",
		},
		{
			name: "ПВЗ не найден", productType: "обувь", none: &noReception{},
			wantErr: domain.ErrPVZNotFound, wantErrMsg: "ПВЗ не найден",
		},
		{name: "ошибка БД при поиске приёмки", productType: "обувь", lockErr: errDB, wantErr: errDB},
		{name: "ошибка БД при добавлении", productType: "обувь", callsSave: true, saveErr: errDB, wantErr: errDB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pvz := newMockPVZRepository(t)
			receptions := newMockReceptionRepository(t)
			products := newMockProductRepository(t)
			tx := newMockTxManager(t)
			if !tt.noTx {
				tx = newTestTx(t, nil)
				expectLockOpen(receptions, pvz, tt.none, tt.lockErr)
			}
			if tt.callsSave {
				products.EXPECT().Create(inTx, want).Return(tt.saveErr)
			}
			// Строгий мок: счётчик, тронутый при ошибке, уронит тест.
			metrics := newMockMetrics(t)
			if tt.wantErr == nil {
				metrics.EXPECT().ProductAdded(tt.productType).Once()
			}
			s := newTestProduct(t, tx, pvz, receptions, products, metrics)
			s.now = func() time.Time { return now }
			s.newID = func() (uuid.UUID, error) { return productID, nil }

			got, err := s.Add(context.Background(), testPVZID, tt.productType)

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

func TestProductDeleteLast(t *testing.T) {
	errDB := errors.New("connection reset")

	tests := []struct {
		name        string
		none        *noReception
		lockErr     error
		callsDelete bool
		deleteErr   error
		wantErr     error
		wantErrMsg  string
	}{
		{name: "товар удалён", callsDelete: true},
		{
			name: "удалять нечего", callsDelete: true, deleteErr: domain.ErrNoProductsToDelete,
			wantErr: domain.ErrNoProductsToDelete, wantErrMsg: "в приёмке нет товаров для удаления",
		},
		{
			name: "нет открытой приёмки", none: &noReception{pvzExists: true},
			wantErr: domain.ErrNoOpenReception, wantErrMsg: "на ПВЗ нет открытой приёмки",
		},
		{
			name: "ПВЗ не найден", none: &noReception{},
			wantErr: domain.ErrPVZNotFound, wantErrMsg: "ПВЗ не найден",
		},
		{name: "ошибка БД при поиске приёмки", lockErr: errDB, wantErr: errDB},
		{name: "ошибка БД при удалении", callsDelete: true, deleteErr: errDB, wantErr: errDB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pvz := newMockPVZRepository(t)
			receptions := newMockReceptionRepository(t)
			products := newMockProductRepository(t)
			expectLockOpen(receptions, pvz, tt.none, tt.lockErr)
			if tt.callsDelete {
				// Удаляется товар именно из заблокированной открытой приёмки.
				products.EXPECT().DeleteLast(inTx, testReceptionID).Return(tt.deleteErr)
			}
			s := newTestProduct(t, newTestTx(t, nil), pvz, receptions, products, newMockMetrics(t))

			err := s.DeleteLast(context.Background(), testPVZID)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				if tt.wantErrMsg != "" {
					assert.EqualError(t, err, tt.wantErrMsg)
				}
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNewProduct(t *testing.T) {
	t.Run("справочник не загрузился", func(t *testing.T) {
		errDB := errors.New("db down")
		types := newMockProductTypeRepository(t)
		types.EXPECT().List(mock.Anything).Return(nil, errDB)

		s, err := NewProduct(context.Background(), newMockTxManager(t), newMockPVZRepository(t),
			newMockReceptionRepository(t), newMockProductRepository(t), types, newMockMetrics(t))

		require.ErrorIs(t, err, errDB)
		assert.Nil(t, s)
	})
}
