// Package service рассчитывает начисления по неизменяемому снимку приёмки.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/HollowKaeden/pvz-service/internal/billing/domain"
	"github.com/HollowKaeden/pvz-service/internal/eventschema"
)

// TxManager открывает транзакцию вокруг бизнес-операции.
type TxManager interface {
	Do(context.Context, func(context.Context) error) error
}

// Repository сохраняет журнал и проекцию; каждый метод — один SQL-запрос.
type Repository interface {
	Rates(context.Context) ([]domain.Rate, error)
	Insert(context.Context, domain.Payout) (bool, error)
	Accrue(context.Context, domain.Payout) error
}

// Metrics получает только результаты завершившихся транзакций.
type Metrics interface {
	Applied(amount int64, duplicate bool)
}

// Payout кэширует справочник ставок при запуске. Для смены ставок нужен рестарт.
type Payout struct {
	tx      TxManager
	repo    Repository
	rates   map[string]int64
	metrics Metrics
}

// Rates возвращает копию ставок, используемых текущим процессом.
func (s *Payout) Rates() []domain.Rate {
	rates := make([]domain.Rate, 0, len(s.rates))
	for k, v := range s.rates {
		rates = append(rates, domain.Rate{ProductType: k, Amount: v})
	}
	sort.Slice(rates, func(i, j int) bool { return rates[i].ProductType < rates[j].ProductType })
	return rates
}

// New загружает ставки из собственной БД billing.
func New(ctx context.Context, tx TxManager, repo Repository, metrics Metrics) (*Payout, error) {
	rates, err := repo.Rates(ctx)
	if err != nil {
		return nil, err
	}
	cache := make(map[string]int64, len(rates))
	for _, r := range rates {
		cache[r.ProductType] = r.Amount
	}
	return &Payout{tx: tx, repo: repo, rates: cache, metrics: metrics}, nil
}

// Apply выполняет ровно INSERT журнала и UPSERT баланса в одной транзакции.
// Уникальный event_id защищает и от повторной, и от конкурентной доставки.
func (s *Payout) Apply(ctx context.Context, data []byte) error {
	p, err := s.calculate(data)
	if err != nil {
		return err
	}
	var inserted bool
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		var err error
		inserted, err = s.repo.Insert(ctx, p)
		if err != nil || !inserted {
			return err
		}
		return s.repo.Accrue(ctx, p)
	})
	if err != nil {
		return fmt.Errorf("начисление: %w", err)
	}
	s.metrics.Applied(p.Amount, !inserted)
	return nil
}

func (s *Payout) calculate(data []byte) (domain.Payout, error) {
	var e eventschema.Event
	if err := json.Unmarshal(data, &e); err != nil {
		return domain.Payout{}, fmt.Errorf("%w: JSON", domain.ErrPermanent)
	}
	p := e.Payload
	if e.ID == uuid.Nil || p.PVZID == uuid.Nil || p.ReceptionID == uuid.Nil ||
		e.Type != eventschema.ReceptionClosedType || e.Version != eventschema.Version ||
		p.City == "" || len(p.City) > 200 || p.OpenedAt.IsZero() || p.ClosedAt.Before(p.OpenedAt) ||
		!e.OccurredAt.Equal(p.ClosedAt) || len(p.Products) == 0 {
		return domain.Payout{}, fmt.Errorf("%w: конверт или приёмка", domain.ErrPermanent)
	}
	var amount int64
	seen := make(map[uuid.UUID]struct{}, len(p.Products))
	for _, product := range p.Products {
		rate, ok := s.rates[product.Type]
		_, duplicate := seen[product.ID]
		if !ok || rate <= 0 || product.ID == uuid.Nil || duplicate || amount > math.MaxInt64-rate {
			return domain.Payout{}, fmt.Errorf("%w: товар или ставка", domain.ErrPermanent)
		}
		seen[product.ID] = struct{}{}
		amount += rate
	}
	return domain.Payout{EventID: e.ID, ReceptionID: p.ReceptionID, PVZID: p.PVZID, City: p.City,
		Amount: amount, Products: len(p.Products), ClosedAt: p.ClosedAt, ProcessedAt: time.Now().UTC()}, nil
}
