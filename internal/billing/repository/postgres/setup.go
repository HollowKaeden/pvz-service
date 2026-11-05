package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	migrations "github.com/HollowKaeden/pvz-service/migrations/billing"
)

// NewPool не выводит строку подключения при ошибке разбора.
func NewPool(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("BILLING_DATABASE_URL: некорректная строка подключения")
	}
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("пул billing: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("подключение к Postgres billing: %w", err)
	}
	return pool, nil
}

// Migrate применяет только миграции billing под блокировкой goose.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.SQL, goose.WithSessionLocker(locker))
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}

// Lock исключает одновременную работу consumer и reset/rebuild. Соединение
// зарезервировано до остановки процесса, блокировка автоматически отпускается.
func Lock(ctx context.Context, pool *pgxpool.Pool) (func(), error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var ok bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(176540016)`).Scan(&ok); err != nil || !ok {
		conn.Release()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("billing уже работает: сначала остановите consumer")
	}
	return func() { _ = conn.Conn().Close(context.WithoutCancel(ctx)); conn.Release() }, nil
}
