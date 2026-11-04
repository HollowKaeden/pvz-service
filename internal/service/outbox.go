package service

import (
	"context"

	"github.com/HollowKaeden/pvz-service/internal/eventschema"
)

// OutboxRepository записывает событие в текущей транзакции. Сервис не знает
// ни о Kafka, ни о том, когда подписчик получит событие.
type OutboxRepository interface {
	Add(ctx context.Context, event eventschema.Event) error
}
