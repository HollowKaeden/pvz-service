// Package postgres — работа с PostgreSQL через pgx: пул соединений, миграции,
// а дальше и репозитории.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pingTimeout — сколько ждать Postgres при старте.
const pingTimeout = 5 * time.Second

// NewPool открывает пул соединений и сразу проверяет связь с БД: если
// Postgres недоступен, сервис падает при запуске, а не на первом запросе.
func NewPool(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		// Текст ошибки pgx может содержать часть строки подключения вместе с
		// паролем, поэтому в лог он не попадает.
		return nil, errors.New("DATABASE_URL: не удалось разобрать строку подключения")
	}
	cfg.MaxConns = maxConns

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("пул Postgres: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("подключение к Postgres: %w", err)
	}
	return pool, nil
}
