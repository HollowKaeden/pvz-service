// Package eventschema — JSON-контракт событий между pvz и его подписчиками.
// Это единственный пакет pvz, который может импортировать billing.
package eventschema

import (
	"time"

	"github.com/google/uuid"
)

// ReceptionClosedType и Version определяют совместимость контракта.
const (
	ReceptionClosedType = "reception.closed"
	Version             = 1
)

// Event — конверт события. ID сохраняется при повторной доставке и нужен
// подписчику для идемпотентности. OccurredAt совпадает с ClosedAt.
type Event struct {
	ID         uuid.UUID       `json:"id"`
	Type       string          `json:"type"`
	Version    int             `json:"version"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    ReceptionClosed `json:"payload"`
}

// ReceptionClosed — неизменяемый состав закрытой приёмки. Подписчику не
// нужны обратные запросы в pvz или доступ к его базе.
type ReceptionClosed struct {
	ReceptionID uuid.UUID `json:"reception_id"`
	PVZID       uuid.UUID `json:"pvz_id"`
	City        string    `json:"city"`
	OpenedAt    time.Time `json:"opened_at"`
	ClosedAt    time.Time `json:"closed_at"`
	Products    []Product `json:"products"`
}

// Product — принятый товар; порядок совпадает с порядком добавления.
type Product struct {
	ID   uuid.UUID `json:"id"`
	Type string    `json:"type"`
}
