// Package demo — эталонные данные публичного демо. Каждую ночь демо
// очищается (pvz demo-reset) и заполняется заново этим набором, чтобы
// посетителю было что посмотреть и с чем поработать.
//
// Данные создаются через сервисы, как это делал бы настоящий клиент, а не
// вставками в таблицы: так они проходят все проверки, а когда появятся
// события о закрытии приёмок, честно дойдут до подписчиков.
package demo

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/HollowKaeden/pvz-service/internal/service"
)

// Services — сервисы, через которые создаётся эталонный набор.
type Services struct {
	PVZ        *service.PVZ
	Receptions *service.Reception
	Products   *service.Product
}

// Seed создаёт эталонный набор в пустой базе:
//   - Москва: закрытая приёмка с 5 товарами и открытая с 2 товарами (её можно
//     сразу закрыть или добавить в неё товар);
//   - Санкт-Петербург: закрытая приёмка с 3 товарами;
//   - Казань: без приёмок (на ней можно открыть первую).
func Seed(ctx context.Context, s Services) error {
	moscow, err := s.PVZ.Create(ctx, "Москва")
	if err != nil {
		return fmt.Errorf("ПВЗ в Москве: %w", err)
	}
	if err := s.reception(ctx, moscow.ID, true, "электроника", "одежда", "обувь", "электроника", "одежда"); err != nil {
		return err
	}
	if err := s.reception(ctx, moscow.ID, false, "обувь", "электроника"); err != nil {
		return err
	}

	spb, err := s.PVZ.Create(ctx, "Санкт-Петербург")
	if err != nil {
		return fmt.Errorf("ПВЗ в Санкт-Петербурге: %w", err)
	}
	if err := s.reception(ctx, spb.ID, true, "одежда", "одежда", "обувь"); err != nil {
		return err
	}

	if _, err := s.PVZ.Create(ctx, "Казань"); err != nil {
		return fmt.Errorf("ПВЗ в Казани: %w", err)
	}
	return nil
}

// reception открывает приёмку на ПВЗ, добавляет товары и, если closed,
// закрывает её.
func (s Services) reception(ctx context.Context, pvzID uuid.UUID, closed bool, productTypes ...string) error {
	if _, err := s.Receptions.Open(ctx, pvzID); err != nil {
		return fmt.Errorf("открытие приёмки: %w", err)
	}
	for _, typ := range productTypes {
		if _, err := s.Products.Add(ctx, pvzID, typ); err != nil {
			return fmt.Errorf("товар %q: %w", typ, err)
		}
	}
	if !closed {
		return nil
	}
	if _, err := s.Receptions.Close(ctx, pvzID); err != nil {
		return fmt.Errorf("закрытие приёмки: %w", err)
	}
	return nil
}
