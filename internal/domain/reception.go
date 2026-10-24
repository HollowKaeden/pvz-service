package domain

import (
	"time"

	"github.com/google/uuid"
)

// ReceptionStatus — статус приёмки товаров.
type ReceptionStatus string

// Статусы из контракта API. Именно close, а не closed: так в swagger.
const (
	ReceptionInProgress ReceptionStatus = "in_progress"
	ReceptionClosed     ReceptionStatus = "close"
)

// Reception — приёмка товаров на ПВЗ. На одном ПВЗ открытой может быть только
// одна приёмка.
type Reception struct {
	ID       uuid.UUID
	PVZID    uuid.UUID
	DateTime time.Time
	Status   ReceptionStatus
}
