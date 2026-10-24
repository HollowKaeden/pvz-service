package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/HollowKaeden/pvz-service/migrations"
)

// Migrate применяет все ещё не применённые миграции из migrations/pvz.
// Пока миграции идут, держится advisory-lock в Postgres: если два экземпляра
// сервиса стартуют одновременно, второй подождёт первого.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	fsys, err := fs.Sub(migrations.PVZ, "pvz")
	if err != nil {
		return fmt.Errorf("миграции: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("миграции: %w", err)
	}

	// goose работает через database/sql. Обёртка берёт соединения из того же
	// пула, а её закрытие пул не закрывает.
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("миграции: %w", err)
	}

	results, err := provider.Up(ctx)
	for _, r := range results {
		if r.Error != nil {
			continue
		}
		log.InfoContext(ctx, "миграция применена",
			slog.Int64("version", r.Source.Version),
			slog.String("file", r.Source.Path),
			slog.Float64("duration_ms", float64(r.Duration.Microseconds())/1000),
		)
	}
	if err != nil {
		return fmt.Errorf("миграции: %w", err)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("миграции: версия схемы: %w", err)
	}
	log.InfoContext(ctx, "схема БД актуальна", slog.Int64("version", version), slog.Int("applied", len(results)))
	return nil
}
