package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// listMocks — репозитории сервиса ПВЗ для тестов списка.
type listMocks struct {
	pvz        *mockPVZRepository
	receptions *mockReceptionRepository
	products   *mockProductRepository
}

func newListService(t *testing.T) (*PVZ, listMocks) {
	t.Helper()
	m := listMocks{
		pvz:        newMockPVZRepository(t),
		receptions: newMockReceptionRepository(t),
		products:   newMockProductRepository(t),
	}
	cities := newMockCityRepository(t)
	cities.EXPECT().List(mock.Anything).Return(testCities, nil)
	s, err := NewPVZ(context.Background(), m.pvz, cities, m.receptions, m.products)
	require.NoError(t, err)
	return s, m
}

func ptr[T any](v T) *T { return &v }

func TestPVZListParams(t *testing.T) {
	day := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		period  domain.Period
		page    int
		limit   int
		wantMsg string
	}{
		{name: "limit 0", page: 1, limit: 0, wantMsg: "limit должен быть от 1 до 30"},
		{name: "limit больше 30", page: 1, limit: 31, wantMsg: "limit должен быть от 1 до 30"},
		{name: "page 0", page: 0, limit: 10, wantMsg: "page должен быть от 1"},
		{name: "page отрицательный", page: -1, limit: 10, wantMsg: "page должен быть от 1"},
		{name: "смещение переполнило бы int", page: math.MaxInt, limit: 30, wantMsg: "page должен быть от 1"},
		{
			name:   "начало позже конца",
			period: domain.Period{Start: ptr(day.Add(time.Hour)), End: ptr(day)},
			page:   1, limit: 10,
			wantMsg: "startDate позже endDate",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newListService(t) // моки упадут, если сервис пойдёт в базу

			got, err := s.List(context.Background(), tt.period, tt.page, tt.limit)

			require.ErrorIs(t, err, domain.ErrInvalidListParams)
			assert.ErrorContains(t, err, tt.wantMsg)
			assert.Nil(t, got)
		})
	}

	t.Run("граничные значения допустимы", func(t *testing.T) {
		s, m := newListService(t)
		period := domain.Period{Start: ptr(day), End: ptr(day)} // один момент времени
		m.pvz.EXPECT().List(mock.Anything, period, 30, 0).Return(nil, nil)
		m.pvz.EXPECT().List(mock.Anything, period, 1, 0).Return(nil, nil)

		_, err := s.List(context.Background(), period, 1, 30)
		require.NoError(t, err)
		_, err = s.List(context.Background(), period, 1, 1)
		require.NoError(t, err)
	})
}

func TestPVZList(t *testing.T) {
	at := func(minute int) time.Time { return time.Date(2026, 10, 24, 12, minute, 0, 0, time.UTC) }
	id := func(n byte) uuid.UUID { return uuid.UUID{15: n} }

	pvz1 := domain.PVZ{ID: id(1), RegistrationDate: at(2), City: "Москва"}
	pvz2 := domain.PVZ{ID: id(2), RegistrationDate: at(1), City: "Казань"}
	// У pvz1 две приёмки (новая сверху, как их отдаёт база), у pvz2 ни одной.
	rec1 := domain.Reception{ID: id(11), PVZID: pvz1.ID, DateTime: at(20), Status: domain.ReceptionInProgress}
	rec2 := domain.Reception{ID: id(12), PVZID: pvz1.ID, DateTime: at(10), Status: domain.ReceptionClosed}
	// Товары только во второй приёмке, в порядке добавления.
	prod1 := domain.Product{ID: id(21), ReceptionID: rec2.ID, DateTime: at(11), Type: "обувь"}
	prod2 := domain.Product{ID: id(22), ReceptionID: rec2.ID, DateTime: at(12), Type: "одежда"}

	period := domain.Period{Start: ptr(at(0))}
	errDB := errors.New("connection reset")

	t.Run("ответ собран из трёх запросов", func(t *testing.T) {
		s, m := newListService(t)
		// Страница 3 по 10: пропустить первые 20 ПВЗ.
		m.pvz.EXPECT().List(mock.Anything, period, 10, 20).Return([]domain.PVZ{pvz1, pvz2}, nil)
		m.receptions.EXPECT().ListByPVZ(mock.Anything, []uuid.UUID{pvz1.ID, pvz2.ID}, period).
			Return([]domain.Reception{rec1, rec2}, nil)
		m.products.EXPECT().ListByReceptions(mock.Anything, []uuid.UUID{rec1.ID, rec2.ID}).
			Return([]domain.Product{prod1, prod2}, nil)

		got, err := s.List(context.Background(), period, 3, 10)

		require.NoError(t, err)
		assert.Equal(t, []domain.PVZWithReceptions{
			{PVZ: pvz1, Receptions: []domain.ReceptionWithProducts{
				{Reception: rec1, Products: []domain.Product{}},
				{Reception: rec2, Products: []domain.Product{prod1, prod2}},
			}},
			{PVZ: pvz2, Receptions: []domain.ReceptionWithProducts{}},
		}, got)
	})
	t.Run("пустая страница: остальные запросы не нужны", func(t *testing.T) {
		s, m := newListService(t)
		m.pvz.EXPECT().List(mock.Anything, domain.Period{}, 10, 0).Return(nil, nil)

		got, err := s.List(context.Background(), domain.Period{}, 1, 10)

		require.NoError(t, err)
		assert.NotNil(t, got, "пустой список, а не nil: в JSON будет [], а не null")
		assert.Empty(t, got)
	})
	t.Run("приёмок нет: товары не запрашиваются", func(t *testing.T) {
		s, m := newListService(t)
		m.pvz.EXPECT().List(mock.Anything, domain.Period{}, 10, 0).Return([]domain.PVZ{pvz2}, nil)
		m.receptions.EXPECT().ListByPVZ(mock.Anything, []uuid.UUID{pvz2.ID}, domain.Period{}).Return(nil, nil)

		got, err := s.List(context.Background(), domain.Period{}, 1, 10)

		require.NoError(t, err)
		assert.Equal(t, []domain.PVZWithReceptions{{PVZ: pvz2, Receptions: []domain.ReceptionWithProducts{}}}, got)
	})

	errorTests := []struct {
		name  string
		setup func(m listMocks)
	}{
		{"ошибка БД при чтении ПВЗ", func(m listMocks) {
			m.pvz.EXPECT().List(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errDB)
		}},
		{"ошибка БД при чтении приёмок", func(m listMocks) {
			m.pvz.EXPECT().List(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]domain.PVZ{pvz1}, nil)
			m.receptions.EXPECT().ListByPVZ(mock.Anything, mock.Anything, mock.Anything).Return(nil, errDB)
		}},
		{"ошибка БД при чтении товаров", func(m listMocks) {
			m.pvz.EXPECT().List(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]domain.PVZ{pvz1}, nil)
			m.receptions.EXPECT().ListByPVZ(mock.Anything, mock.Anything, mock.Anything).
				Return([]domain.Reception{rec1}, nil)
			m.products.EXPECT().ListByReceptions(mock.Anything, mock.Anything).Return(nil, errDB)
		}},
	}
	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			s, m := newListService(t)
			tt.setup(m)

			got, err := s.List(context.Background(), domain.Period{}, 1, 10)

			require.ErrorIs(t, err, errDB)
			assert.Nil(t, got)
		})
	}
}
