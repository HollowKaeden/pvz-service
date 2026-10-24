package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// PVZRepository хранит ПВЗ.
type PVZRepository interface {
	Create(ctx context.Context, pvz domain.PVZ) error
}

// CityRepository читает справочник городов.
type CityRepository interface {
	List(ctx context.Context) ([]string, error)
}

// PVZ — заведение ПВЗ и чтение списка.
type PVZ struct {
	repo   PVZRepository
	cities []string // справочник городов, загружается один раз при старте

	now   func() time.Time
	newID func() (uuid.UUID, error)
}

// NewPVZ создаёт сервис и загружает справочник городов. Справочник меняется
// только миграциями, поэтому читать его на каждый запрос незачем.
func NewPVZ(ctx context.Context, repo PVZRepository, cities CityRepository) (*PVZ, error) {
	list, err := cities.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("справочник городов: %w", err)
	}
	return &PVZ{repo: repo, cities: list, now: time.Now, newID: uuid.NewV7}, nil
}

// Create заводит ПВЗ в городе из справочника. Идентификатор и дату
// регистрации назначает сервис.
func (s *PVZ) Create(ctx context.Context, city string) (domain.PVZ, error) {
	if !slices.Contains(s.cities, city) {
		return domain.PVZ{}, fmt.Errorf("%w: %q (доступны: %s)",
			domain.ErrCityNotAllowed, city, strings.Join(s.cities, ", "))
	}

	id, err := s.newID()
	if err != nil {
		return domain.PVZ{}, fmt.Errorf("id ПВЗ: %w", err)
	}
	pvz := domain.PVZ{
		ID: id,
		// Postgres хранит время с точностью до микросекунд: обрезаем сразу,
		// чтобы ответ совпадал с тем, что потом прочитается из базы.
		RegistrationDate: s.now().UTC().Truncate(time.Microsecond),
		City:             city,
	}
	if err := s.repo.Create(ctx, pvz); err != nil {
		return domain.PVZ{}, fmt.Errorf("создание ПВЗ: %w", err)
	}
	return pvz, nil
}
