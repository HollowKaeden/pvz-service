package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// PVZRepository хранит ПВЗ в таблице pvz.
type PVZRepository struct {
	db *DB
}

// NewPVZRepository создаёт репозиторий ПВЗ.
func NewPVZRepository(db *DB) *PVZRepository {
	return &PVZRepository{db: db}
}

// Create сохраняет ПВЗ. Город хранится ссылкой на справочник: id города
// находится по названию в том же запросе.
func (r *PVZRepository) Create(ctx context.Context, pvz domain.PVZ) error {
	tag, err := r.db.conn(ctx).Exec(ctx, `
		INSERT INTO pvz (id, registration_date, city_id)
		SELECT $1, $2, c.id FROM cities c WHERE c.name = $3`,
		pvz.ID, pvz.RegistrationDate, pvz.City,
	)
	if err != nil {
		return fmt.Errorf("insert pvz: %w", err)
	}
	// Сервис проверил город по справочнику, так что 0 строк значит, что
	// справочник в базе и в памяти разошёлся.
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("insert pvz: города %q нет в справочнике", pvz.City)
	}
	return nil
}

// CityRepository читает справочник городов.
type CityRepository struct {
	db *DB
}

// NewCityRepository создаёт репозиторий справочника городов.
func NewCityRepository(db *DB) *CityRepository {
	return &CityRepository{db: db}
}

// List возвращает названия городов в порядке справочника.
func (r *CityRepository) List(ctx context.Context) ([]string, error) {
	rows, err := r.db.conn(ctx).Query(ctx, `SELECT name FROM cities ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("select cities: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("select cities: %w", err)
	}
	return names, nil
}
