package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// ReceptionRepository хранит приёмки.
type ReceptionRepository interface {
	Create(ctx context.Context, rec domain.Reception) error
	// LockOpen блокирует открытую приёмку ПВЗ до конца транзакции. Если её нет,
	// возвращает domain.ErrNoOpenReception.
	LockOpen(ctx context.Context, pvzID uuid.UUID) (domain.Reception, error)
	SetStatus(ctx context.Context, id uuid.UUID, status domain.ReceptionStatus) error
	// ListByPVZ возвращает приёмки этих ПВЗ, новые сверху. Если период задан,
	// только приёмки из периода.
	ListByPVZ(ctx context.Context, pvzIDs []uuid.UUID, period domain.Period) ([]domain.Reception, error)
}

// TxManager выполняет fn в транзакции: коммит, если fn вернула nil, иначе
// откат. Транзакция передаётся репозиториям через ctx.
type TxManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Reception — открытие и закрытие приёмок. Ошибки бизнес-правил из
// репозиториев возвращаются без обёртки: их текст показывается клиенту.
type Reception struct {
	tx         TxManager
	pvz        PVZRepository
	receptions ReceptionRepository

	now   func() time.Time
	newID func() (uuid.UUID, error)
}

// NewReception создаёт сервис приёмок.
func NewReception(tx TxManager, pvz PVZRepository, receptions ReceptionRepository) *Reception {
	return &Reception{tx: tx, pvz: pvz, receptions: receptions, now: time.Now, newID: uuid.NewV7}
}

// Open открывает новую приёмку на ПВЗ, если на нём нет незакрытой.
//
// Проверка и вставка идут в одной транзакции под блокировкой строки ПВЗ.
// Без неё два одновременных запроса оба увидели бы, что открытой приёмки нет,
// и оба открыли бы свою. С блокировкой второй запрос ждёт, пока первый
// закончит, и уже видит открытую им приёмку.
func (s *Reception) Open(ctx context.Context, pvzID uuid.UUID) (domain.Reception, error) {
	id, err := s.newID()
	if err != nil {
		return domain.Reception{}, fmt.Errorf("id приёмки: %w", err)
	}

	var rec domain.Reception
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		if err := s.pvz.Lock(ctx, pvzID); err != nil {
			if errors.Is(err, domain.ErrPVZNotFound) {
				return err
			}
			return fmt.Errorf("блокировка ПВЗ: %w", err)
		}

		_, err := s.receptions.LockOpen(ctx, pvzID)
		switch {
		case err == nil:
			return domain.ErrReceptionAlreadyOpen
		case !errors.Is(err, domain.ErrNoOpenReception):
			return fmt.Errorf("поиск открытой приёмки: %w", err)
		}

		rec = domain.Reception{
			ID:    id,
			PVZID: pvzID,
			// Postgres хранит время с точностью до микросекунд: обрезаем сразу,
			// чтобы ответ совпадал с тем, что потом прочитается из базы.
			DateTime: s.now().UTC().Truncate(time.Microsecond),
			Status:   domain.ReceptionInProgress,
		}
		if err := s.receptions.Create(ctx, rec); err != nil {
			if errors.Is(err, domain.ErrReceptionAlreadyOpen) {
				return err
			}
			return fmt.Errorf("создание приёмки: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.Reception{}, err
	}
	return rec, nil
}

// Close закрывает открытую приёмку ПВЗ. Приёмка блокируется до конца
// транзакции, поэтому закрытие не пересечётся с добавлением или удалением
// товара в ней.
func (s *Reception) Close(ctx context.Context, pvzID uuid.UUID) (domain.Reception, error) {
	var rec domain.Reception
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		var err error
		rec, err = lockOpenReception(ctx, s.receptions, s.pvz, pvzID)
		if err != nil {
			return err
		}

		if err := s.receptions.SetStatus(ctx, rec.ID, domain.ReceptionClosed); err != nil {
			return fmt.Errorf("закрытие приёмки: %w", err)
		}
		rec.Status = domain.ReceptionClosed
		return nil
	})
	if err != nil {
		return domain.Reception{}, err
	}
	return rec, nil
}

// lockOpenReception блокирует открытую приёмку ПВЗ до конца транзакции. Общий
// первый шаг закрытия приёмки, добавления и удаления товара.
//
// Если открытой приёмки нет, уточняет, есть ли вообще такой ПВЗ: клиенту это
// разные ответы. Лишний запрос делается только в этом случае.
func lockOpenReception(
	ctx context.Context, receptions ReceptionRepository, pvz PVZRepository, pvzID uuid.UUID,
) (domain.Reception, error) {
	rec, err := receptions.LockOpen(ctx, pvzID)
	if errors.Is(err, domain.ErrNoOpenReception) {
		exists, err := pvz.Exists(ctx, pvzID)
		if err != nil {
			return domain.Reception{}, fmt.Errorf("проверка ПВЗ: %w", err)
		}
		if !exists {
			return domain.Reception{}, domain.ErrPVZNotFound
		}
		return domain.Reception{}, domain.ErrNoOpenReception
	}
	if err != nil {
		return domain.Reception{}, fmt.Errorf("поиск открытой приёмки: %w", err)
	}
	return rec, nil
}
