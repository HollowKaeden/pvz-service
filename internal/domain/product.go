package domain

import (
	"time"

	"github.com/google/uuid"
)

// Product — товар, принятый в рамках приёмки. Тип товара — название из
// справочника типов.
type Product struct {
	ID          uuid.UUID
	ReceptionID uuid.UUID
	DateTime    time.Time
	Type        string
}
