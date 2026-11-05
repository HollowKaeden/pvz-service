// Package postgres работает только с БД billing через pgx, без ORM.
package postgres

import (
	"context"
	"fmt"

	trmpgx "github.com/avito-tech/go-transaction-manager/drivers/pgxv5/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HollowKaeden/pvz-service/internal/billing/domain"
)

// Repository использует транзакцию из контекста сервисного слоя.
type Repository struct{ pool *pgxpool.Pool }

// New создаёт репозиторий начислений.
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) conn(ctx context.Context) trmpgx.Tr {
	return trmpgx.DefaultCtxGetter.DefaultTrOrDB(ctx, r.pool)
}

// Rates читает справочник (на горячем пути не вызывается).
func (r *Repository) Rates(ctx context.Context) ([]domain.Rate, error) {
	rows, err := r.conn(ctx).Query(ctx, `SELECT product_type, amount_kopecks FROM rates ORDER BY product_type`)
	if err != nil {
		return nil, fmt.Errorf("ставки: %w", err)
	}
	defer rows.Close()
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Rate, error) {
		var v domain.Rate
		err := row.Scan(&v.ProductType, &v.Amount)
		return v, err
	})
}

// Insert возвращает false для уже обработанного UUID.
func (r *Repository) Insert(ctx context.Context, p domain.Payout) (bool, error) {
	tag, err := r.conn(ctx).Exec(ctx, `INSERT INTO payouts
		(event_id, reception_id, pvz_id, city, amount_kopecks, products, closed_at, processed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (event_id) DO NOTHING`,
		p.EventID, p.ReceptionID, p.PVZID, p.City, p.Amount, p.Products, p.ClosedAt, p.ProcessedAt)
	if err != nil {
		return false, fmt.Errorf("журнал: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Accrue обновляет баланс; вызывается только после успешного Insert в той же tx.
func (r *Repository) Accrue(ctx context.Context, p domain.Payout) error {
	_, err := r.conn(ctx).Exec(ctx, `INSERT INTO balances (pvz_id,city,amount_kopecks,updated_at)
		VALUES ($1,$2,$3,$4) ON CONFLICT (pvz_id) DO UPDATE SET
		amount_kopecks=balances.amount_kopecks+EXCLUDED.amount_kopecks,
		city=EXCLUDED.city, updated_at=GREATEST(balances.updated_at,EXCLUDED.updated_at)`, p.PVZID, p.City, p.Amount, p.ProcessedAt)
	if err != nil {
		return fmt.Errorf("баланс: %w", err)
	}
	return nil
}

// Balances читает страницу проекции в стабильном порядке первичного ключа.
func (r *Repository) Balances(ctx context.Context, limit, offset int) ([]domain.Balance, error) {
	rows, err := r.pool.Query(ctx, `SELECT pvz_id,city,amount_kopecks,updated_at FROM balances ORDER BY pvz_id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Balance, error) {
		var v domain.Balance
		err := row.Scan(&v.PVZID, &v.City, &v.Amount, &v.UpdatedAt)
		return v, err
	})
}

// Journal использует составной индекс по ПВЗ и времени начисления.
func (r *Repository) Journal(ctx context.Context, id uuid.UUID, limit, offset int) ([]domain.Payout, error) {
	rows, err := r.pool.Query(ctx, `SELECT event_id,reception_id,pvz_id,city,amount_kopecks,products,closed_at,processed_at
		FROM payouts WHERE pvz_id=$1 ORDER BY processed_at DESC,event_id DESC LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Payout, error) {
		var v domain.Payout
		err := row.Scan(&v.EventID, &v.ReceptionID, &v.PVZID, &v.City, &v.Amount, &v.Products, &v.ClosedAt, &v.ProcessedAt)
		return v, err
	})
}
