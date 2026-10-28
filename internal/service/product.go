package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// ProductRepository хранит товары.
type ProductRepository interface {
	Create(ctx context.Context, p domain.Product) error
	// DeleteLast удаляет последний добавленный товар приёмки. Если товаров
	// нет, возвращает domain.ErrNoProductsToDelete.
	DeleteLast(ctx context.Context, receptionID uuid.UUID) error
	// ListByReceptions возвращает товары этих приёмок в порядке добавления.
	ListByReceptions(ctx context.Context, receptionIDs []uuid.UUID) ([]domain.Product, error)
}

// ProductTypeRepository читает справочник типов товаров.
type ProductTypeRepository interface {
	List(ctx context.Context) ([]string, error)
}

// Product — добавление товаров в открытую приёмку и удаление последнего
// добавленного (LIFO). Ошибки бизнес-правил из репозиториев возвращаются без
// обёртки: их текст показывается клиенту.
//
// Обе операции идут в транзакции под блокировкой открытой приёмки. Поэтому
// приёмку нельзя закрыть посреди добавления товара, а два одновременных
// удаления не удалят один и тот же товар и не пропустят ни одного.
type Product struct {
	tx         TxManager
	pvz        PVZRepository
	receptions ReceptionRepository
	products   ProductRepository
	metrics    Metrics
	types      []string // справочник типов товаров, загружается один раз при старте

	now   func() time.Time
	newID func() (uuid.UUID, error)
}

// NewProduct создаёт сервис и загружает справочник типов товаров. Справочник
// меняется только миграциями, поэтому читать его на каждый запрос незачем.
func NewProduct(
	ctx context.Context,
	tx TxManager,
	pvz PVZRepository,
	receptions ReceptionRepository,
	products ProductRepository,
	types ProductTypeRepository,
	metrics Metrics,
) (*Product, error) {
	list, err := types.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("справочник типов товаров: %w", err)
	}
	return &Product{
		tx:         tx,
		pvz:        pvz,
		receptions: receptions,
		products:   products,
		metrics:    metrics,
		types:      list,
		now:        time.Now,
		newID:      uuid.NewV7,
	}, nil
}

// Add добавляет товар в открытую приёмку ПВЗ. Идентификатор и время
// добавления назначает сервис.
func (s *Product) Add(ctx context.Context, pvzID uuid.UUID, productType string) (domain.Product, error) {
	if !slices.Contains(s.types, productType) {
		return domain.Product{}, fmt.Errorf("%w: %q (доступны: %s)",
			domain.ErrProductTypeNotAllowed, productType, strings.Join(s.types, ", "))
	}
	id, err := s.newID()
	if err != nil {
		return domain.Product{}, fmt.Errorf("id товара: %w", err)
	}

	var p domain.Product
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		rec, err := lockOpenReception(ctx, s.receptions, s.pvz, pvzID)
		if err != nil {
			return err
		}

		p = domain.Product{
			ID:          id,
			ReceptionID: rec.ID,
			// Postgres хранит время с точностью до микросекунд: обрезаем сразу,
			// чтобы ответ совпадал с тем, что потом прочитается из базы.
			DateTime: s.now().UTC().Truncate(time.Microsecond),
			Type:     productType,
		}
		if err := s.products.Create(ctx, p); err != nil {
			return fmt.Errorf("добавление товара: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.Product{}, err
	}
	s.metrics.ProductAdded(productType)
	return p, nil
}

// DeleteLast удаляет из открытой приёмки ПВЗ последний добавленный товар.
func (s *Product) DeleteLast(ctx context.Context, pvzID uuid.UUID) error {
	return s.tx.Do(ctx, func(ctx context.Context) error {
		rec, err := lockOpenReception(ctx, s.receptions, s.pvz, pvzID)
		if err != nil {
			return err
		}

		err = s.products.DeleteLast(ctx, rec.ID)
		if errors.Is(err, domain.ErrNoProductsToDelete) {
			return err
		}
		if err != nil {
			return fmt.Errorf("удаление товара: %w", err)
		}
		return nil
	})
}
