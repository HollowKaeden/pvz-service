package service

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// PVZRepository хранит ПВЗ.
type PVZRepository interface {
	Create(ctx context.Context, pvz domain.PVZ) error
	// Lock блокирует строку ПВЗ до конца транзакции. Если ПВЗ нет, возвращает
	// domain.ErrPVZNotFound.
	Lock(ctx context.Context, id uuid.UUID) error
	Exists(ctx context.Context, id uuid.UUID) (bool, error)
	// List возвращает страницу ПВЗ, новые сверху. Если период задан, только
	// ПВЗ с приёмками в этом периоде.
	List(ctx context.Context, period domain.Period, limit, offset int) ([]domain.PVZ, error)
}

// CityRepository читает справочник городов.
type CityRepository interface {
	List(ctx context.Context) ([]string, error)
}

// PVZ — заведение ПВЗ и чтение списка.
type PVZ struct {
	repo       PVZRepository
	receptions ReceptionRepository
	products   ProductRepository
	cities     []string // справочник городов, загружается один раз при старте

	now   func() time.Time
	newID func() (uuid.UUID, error)
}

// NewPVZ создаёт сервис и загружает справочник городов. Справочник меняется
// только миграциями, поэтому читать его на каждый запрос незачем.
func NewPVZ(
	ctx context.Context,
	repo PVZRepository,
	cities CityRepository,
	receptions ReceptionRepository,
	products ProductRepository,
) (*PVZ, error) {
	list, err := cities.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("справочник городов: %w", err)
	}
	return &PVZ{
		repo:       repo,
		receptions: receptions,
		products:   products,
		cities:     list,
		now:        time.Now,
		newID:      uuid.NewV7,
	}, nil
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

// maxLimit — наибольший размер страницы списка ПВЗ (по контракту).
const maxLimit = 30

// List возвращает страницу ПВЗ с приёмками и товарами, новые ПВЗ и приёмки
// сверху. Если задан период, в список попадают только ПВЗ, у которых были
// приёмки в этом периоде, и у них показываются только эти приёмки.
//
// Данные читаются тремя запросами, сколько бы ни было ПВЗ на странице:
// страница ПВЗ, затем приёмки сразу всех этих ПВЗ, затем товары сразу всех
// этих приёмок. Ответ собирается в памяти. Отдельный запрос на каждый ПВЗ и
// каждую приёмку (N+1) на странице из 30 ПВЗ дал бы сотни запросов.
func (s *PVZ) List(ctx context.Context, period domain.Period, page, limit int) ([]domain.PVZWithReceptions, error) {
	if err := validateListParams(period, page, limit); err != nil {
		return nil, err
	}

	pvzs, err := s.repo.List(ctx, period, limit, (page-1)*limit)
	if err != nil {
		return nil, fmt.Errorf("список ПВЗ: %w", err)
	}
	if len(pvzs) == 0 {
		return []domain.PVZWithReceptions{}, nil
	}

	pvzIDs := make([]uuid.UUID, len(pvzs))
	for i, p := range pvzs {
		pvzIDs[i] = p.ID
	}
	receptions, err := s.receptions.ListByPVZ(ctx, pvzIDs, period)
	if err != nil {
		return nil, fmt.Errorf("приёмки ПВЗ: %w", err)
	}

	var products []domain.Product
	if len(receptions) > 0 {
		receptionIDs := make([]uuid.UUID, len(receptions))
		for i, r := range receptions {
			receptionIDs[i] = r.ID
		}
		products, err = s.products.ListByReceptions(ctx, receptionIDs)
		if err != nil {
			return nil, fmt.Errorf("товары приёмок: %w", err)
		}
	}

	return assemblePVZList(pvzs, receptions, products), nil
}

// validateListParams проверяет страницу, её размер и период.
func validateListParams(period domain.Period, page, limit int) error {
	if limit < 1 || limit > maxLimit {
		return fmt.Errorf("%w: limit должен быть от 1 до %d", domain.ErrInvalidListParams, maxLimit)
	}
	// Верхняя граница page нужна, чтобы смещение (page-1)*limit не
	// переполнило int.
	if page < 1 || page > math.MaxInt/maxLimit {
		return fmt.Errorf("%w: page должен быть от 1", domain.ErrInvalidListParams)
	}
	if period.Start != nil && period.End != nil && period.Start.After(*period.End) {
		return fmt.Errorf("%w: startDate позже endDate", domain.ErrInvalidListParams)
	}
	return nil
}

// assemblePVZList раскладывает приёмки по ПВЗ и товары по приёмкам. Порядок
// внутри каждой группы тот же, в каком их вернула база. Списки приёмок и
// товаров не бывают nil: в ответе пустой список, а не null.
func assemblePVZList(
	pvzs []domain.PVZ, receptions []domain.Reception, products []domain.Product,
) []domain.PVZWithReceptions {
	productsByReception := make(map[uuid.UUID][]domain.Product, len(receptions))
	for _, p := range products {
		productsByReception[p.ReceptionID] = append(productsByReception[p.ReceptionID], p)
	}

	receptionsByPVZ := make(map[uuid.UUID][]domain.ReceptionWithProducts, len(pvzs))
	for _, r := range receptions {
		items := productsByReception[r.ID]
		if items == nil {
			items = []domain.Product{}
		}
		receptionsByPVZ[r.PVZID] = append(receptionsByPVZ[r.PVZID],
			domain.ReceptionWithProducts{Reception: r, Products: items})
	}

	list := make([]domain.PVZWithReceptions, len(pvzs))
	for i, p := range pvzs {
		items := receptionsByPVZ[p.ID]
		if items == nil {
			items = []domain.ReceptionWithProducts{}
		}
		list[i] = domain.PVZWithReceptions{PVZ: p, Receptions: items}
	}
	return list
}
