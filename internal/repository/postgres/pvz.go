package postgres

import (
	"context"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
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

// Lock блокирует строку ПВЗ до конца транзакции: другие транзакции, которые
// хотят заблокировать тот же ПВЗ, ждут её коммита или отката. Вызывается только
// внутри транзакции, иначе блокировка снимается сразу после запроса.
func (r *PVZRepository) Lock(ctx context.Context, id uuid.UUID) error {
	var locked uuid.UUID
	err := r.db.conn(ctx).QueryRow(ctx, `SELECT id FROM pvz WHERE id = $1 FOR UPDATE`, id).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrPVZNotFound
	}
	if err != nil {
		return fmt.Errorf("lock pvz: %w", err)
	}
	return nil
}

// Exists сообщает, есть ли ПВЗ с таким id.
func (r *PVZRepository) Exists(ctx context.Context, id uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.conn(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pvz WHERE id = $1)`, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("pvz exists: %w", err)
	}
	return exists, nil
}

// List возвращает страницу ПВЗ, новые сверху. Если период задан, в список
// попадают только ПВЗ, у которых есть приёмки в этом периоде.
func (r *PVZRepository) List(ctx context.Context, period domain.Period, limit, offset int) ([]domain.PVZ, error) {
	q := psql().
		Select("p.id", "p.registration_date", "c.name").
		From("pvz p").
		Join("cities c ON c.id = p.city_id").
		// id в конце сортировки делает порядок однозначным, даже если у двух
		// ПВЗ совпадёт дата регистрации: иначе страницы могли бы пересекаться.
		OrderBy("p.registration_date DESC", "p.id DESC").
		Suffix("LIMIT ? OFFSET ?", limit, offset)
	if period.IsSet() {
		// Приёмки ПВЗ за период ищутся по индексу (pvz_id, date_time).
		q = q.Where(sq.Expr("EXISTS (?)", sq.
			Select("1").
			From("receptions r").
			Where("r.pvz_id = p.id").
			Where(inPeriod("r.date_time", period)),
		))
	}
	query, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("select pvz: %w", err)
	}

	rows, err := r.db.conn(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select pvz: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.PVZ, error) {
		var p domain.PVZ
		err := row.Scan(&p.ID, &p.RegistrationDate, &p.City)
		// pgx отдаёт timestamptz в часовом поясе процесса, а сервис везде
		// работает в UTC.
		p.RegistrationDate = p.RegistrationDate.UTC()
		return p, err
	})
	if err != nil {
		return nil, fmt.Errorf("select pvz: %w", err)
	}
	return list, nil
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
