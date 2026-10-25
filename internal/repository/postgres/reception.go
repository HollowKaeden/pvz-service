package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// uniqueViolation — код ошибки Postgres «нарушена уникальность».
const uniqueViolation = "23505"

// ReceptionRepository хранит приёмки в таблице receptions.
type ReceptionRepository struct {
	db *DB
}

// NewReceptionRepository создаёт репозиторий приёмок.
func NewReceptionRepository(db *DB) *ReceptionRepository {
	return &ReceptionRepository{db: db}
}

// Create сохраняет приёмку. Статус хранится ссылкой на справочник: id статуса
// находится по названию в том же запросе.
func (r *ReceptionRepository) Create(ctx context.Context, rec domain.Reception) error {
	tag, err := r.db.conn(ctx).Exec(ctx, `
		INSERT INTO receptions (id, pvz_id, date_time, status_id)
		SELECT $1, $2, $3, s.id FROM reception_statuses s WHERE s.name = $4`,
		rec.ID, rec.PVZID, rec.DateTime, rec.Status,
	)
	// Сработал уникальный индекс на открытую приёмку. Сервис проверяет это
	// раньше под блокировкой ПВЗ, так что сюда попадёт только запись в обход
	// сервиса. Ответ тот же, что и при проверке в сервисе.
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
		pgErr.Code == uniqueViolation && pgErr.ConstraintName == "receptions_one_open_per_pvz_idx" {
		return domain.ErrReceptionAlreadyOpen
	}
	if err != nil {
		return fmt.Errorf("insert reception: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("insert reception: статуса %q нет в справочнике", rec.Status)
	}
	return nil
}

// LockOpen находит открытую приёмку ПВЗ и блокирует её строку до конца
// транзакции. Если открытой приёмки нет, возвращает domain.ErrNoOpenReception.
// Вызывается только внутри транзакции.
func (r *ReceptionRepository) LockOpen(ctx context.Context, pvzID uuid.UUID) (domain.Reception, error) {
	rec := domain.Reception{Status: domain.ReceptionInProgress}
	// Открытая приёмка на ПВЗ одна (это гарантирует уникальный индекс), но
	// ORDER BY и LIMIT оставлены: ищем последнюю открытую, как в задании, и
	// запрос идёт по индексу (pvz_id, date_time).
	err := r.db.conn(ctx).QueryRow(ctx, `
		SELECT r.id, r.pvz_id, r.date_time
		FROM receptions r
		JOIN reception_statuses s ON s.id = r.status_id
		WHERE r.pvz_id = $1 AND s.name = $2
		ORDER BY r.date_time DESC
		LIMIT 1
		FOR UPDATE OF r`,
		pvzID, domain.ReceptionInProgress,
	).Scan(&rec.ID, &rec.PVZID, &rec.DateTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Reception{}, domain.ErrNoOpenReception
	}
	if err != nil {
		return domain.Reception{}, fmt.Errorf("lock open reception: %w", err)
	}
	// pgx отдаёт timestamptz в часовом поясе процесса, а сервис везде
	// работает в UTC.
	rec.DateTime = rec.DateTime.UTC()
	return rec, nil
}

// ListByPVZ возвращает приёмки этих ПВЗ, новые сверху. Если период задан,
// только приёмки из периода.
func (r *ReceptionRepository) ListByPVZ(
	ctx context.Context, pvzIDs []uuid.UUID, period domain.Period,
) ([]domain.Reception, error) {
	// Ищется по индексу (pvz_id, date_time).
	q := psql().
		Select("r.id", "r.pvz_id", "r.date_time", "s.name").
		From("receptions r").
		Join("reception_statuses s ON s.id = r.status_id").
		Where("r.pvz_id = ANY(?)", pvzIDs).
		OrderBy("r.date_time DESC")
	if period.IsSet() {
		q = q.Where(inPeriod("r.date_time", period))
	}
	query, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("select receptions: %w", err)
	}

	rows, err := r.db.conn(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select receptions: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Reception, error) {
		var rec domain.Reception
		err := row.Scan(&rec.ID, &rec.PVZID, &rec.DateTime, &rec.Status)
		rec.DateTime = rec.DateTime.UTC()
		return rec, err
	})
	if err != nil {
		return nil, fmt.Errorf("select receptions: %w", err)
	}
	return list, nil
}

// HasProducts сообщает, есть ли в приёмке хотя бы один товар. Ищется по
// индексу (reception_id, seq).
func (r *ReceptionRepository) HasProducts(ctx context.Context, id uuid.UUID) (bool, error) {
	var has bool
	err := r.db.conn(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM products WHERE reception_id = $1)`, id).Scan(&has)
	if err != nil {
		return false, fmt.Errorf("reception has products: %w", err)
	}
	return has, nil
}

// SetStatus меняет статус приёмки.
func (r *ReceptionRepository) SetStatus(ctx context.Context, id uuid.UUID, status domain.ReceptionStatus) error {
	tag, err := r.db.conn(ctx).Exec(ctx, `
		UPDATE receptions SET status_id = s.id
		FROM reception_statuses s
		WHERE receptions.id = $1 AND s.name = $2`,
		id, status,
	)
	if err != nil {
		return fmt.Errorf("update reception status: %w", err)
	}
	// Сервис меняет статус только у приёмки, которую сам заблокировал, так что
	// 0 строк значит, что приёмки нет или статуса нет в справочнике.
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("update reception status: приёмка %s или статус %q не найдены", id, status)
	}
	return nil
}
