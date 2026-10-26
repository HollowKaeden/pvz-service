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

var testCities = []string{"Москва", "Санкт-Петербург", "Казань"}

// newTestPVZ создаёт сервис с фиксированными временем и id.
func newTestPVZ(t *testing.T, repo PVZRepository, now time.Time, id uuid.UUID) *PVZ {
	t.Helper()
	cities := newMockCityRepository(t)
	cities.EXPECT().List(mock.Anything).Return(testCities, nil)

	s, err := NewPVZ(context.Background(), repo, cities, newMockReceptionRepository(t), newMockProductRepository(t))
	require.NoError(t, err)
	s.now = func() time.Time { return now }
	s.newID = func() (uuid.UUID, error) { return id, nil }
	return s
}

func TestPVZCreate(t *testing.T) {
	id := uuid.MustParse("0192f1a4-7b3c-7d2e-8f00-000000000001")
	// Время с наносекундами и не в UTC: сервис должен привести его к виду,
	// в котором оно хранится в Postgres.
	now := time.Date(2026, 10, 24, 15, 4, 5, 123456789, time.FixedZone("MSK", 3*60*60))
	want := domain.PVZ{
		ID:               id,
		RegistrationDate: time.Date(2026, 10, 24, 12, 4, 5, 123456000, time.UTC),
		City:             "Казань",
	}
	errDB := errors.New("connection reset")

	tests := []struct {
		name    string
		city    string
		repoErr error
		callsDB bool
		want    domain.PVZ
		wantErr error
	}{
		{name: "город из справочника", city: "Казань", callsDB: true, want: want},
		{name: "города нет в справочнике", city: "Томск", wantErr: domain.ErrCityNotAllowed},
		{name: "город не указан", city: "", wantErr: domain.ErrCityNotAllowed},
		{name: "регистр важен", city: "казань", wantErr: domain.ErrCityNotAllowed},
		{name: "ошибка БД", city: "Казань", callsDB: true, repoErr: errDB, wantErr: errDB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockPVZRepository(t) // проверяет, что ожидаемые вызовы были, а лишних нет
			if tt.callsDB {
				repo.EXPECT().Create(mock.Anything, want).Return(tt.repoErr)
			}
			s := newTestPVZ(t, repo, now, id)

			got, err := s.Create(context.Background(), tt.city)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Zero(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPVZCreateErrorMessage(t *testing.T) {
	s := newTestPVZ(t, newMockPVZRepository(t), time.Now(), uuid.New())

	_, err := s.Create(context.Background(), "Томск")

	assert.EqualError(t, err,
		`город недоступен для открытия ПВЗ: "Томск" (доступны: Москва, Санкт-Петербург, Казань)`)
}

func TestNewPVZ(t *testing.T) {
	t.Run("справочник не загрузился", func(t *testing.T) {
		errDB := errors.New("db down")
		cities := newMockCityRepository(t)
		cities.EXPECT().List(mock.Anything).Return(nil, errDB)

		s, err := NewPVZ(context.Background(), newMockPVZRepository(t), cities,
			newMockReceptionRepository(t), newMockProductRepository(t))

		require.ErrorIs(t, err, errDB)
		assert.Nil(t, s)
	})
}

func TestPVZAll(t *testing.T) {
	list := []domain.PVZ{
		{ID: uuid.MustParse("0192f1a4-7b3c-7d2e-8f00-000000000002"), City: "Москва"},
		{ID: uuid.MustParse("0192f1a4-7b3c-7d2e-8f00-000000000001"), City: "Казань"},
	}
	errDB := errors.New("connection reset")

	t.Run("все ПВЗ", func(t *testing.T) {
		repo := newMockPVZRepository(t)
		repo.EXPECT().All(mock.Anything).Return(list, nil)

		got, err := newTestPVZ(t, repo, time.Now(), uuid.Nil).All(context.Background())

		require.NoError(t, err)
		assert.Equal(t, list, got)
	})
	t.Run("ошибка БД", func(t *testing.T) {
		repo := newMockPVZRepository(t)
		repo.EXPECT().All(mock.Anything).Return(nil, errDB)

		got, err := newTestPVZ(t, repo, time.Now(), uuid.Nil).All(context.Background())

		require.ErrorIs(t, err, errDB)
		assert.Nil(t, got)
	})
}
