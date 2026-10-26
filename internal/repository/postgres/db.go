package postgres

import (
	"context"
	"fmt"

	trmpgx "github.com/avito-tech/go-transaction-manager/drivers/pgxv5/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB — общая основа репозиториев: пул соединений и getter менеджера
// транзакций. Если сервис открыл транзакцию, она лежит в context, и
// репозиторий выполняет запрос в ней; иначе запрос идёт через пул. Сам
// репозиторий транзакций не открывает: это решает сервис.
type DB struct {
	pool   *pgxpool.Pool
	getter *trmpgx.CtxGetter
}

// NewDB создаёт основу репозиториев. Getter по умолчанию работает в паре с
// менеджером транзакций по умолчанию (trm/v2/manager).
func NewDB(pool *pgxpool.Pool) *DB {
	return &DB{pool: pool, getter: trmpgx.DefaultCtxGetter}
}

// ClearData удаляет все ПВЗ, приёмки, товары и пользователей. Справочники не
// трогает. Нужна только для сброса публичного демо (pvz demo-reset).
func ClearData(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `TRUNCATE products, receptions, pvz, users`); err != nil {
		return fmt.Errorf("очистка данных: %w", err)
	}
	return nil
}

// conn возвращает текущую транзакцию из ctx или пул.
func (db *DB) conn(ctx context.Context) trmpgx.Tr {
	return db.getter.DefaultTrOrDB(ctx, db.pool)
}
