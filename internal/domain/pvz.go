package domain

import (
	"time"

	"github.com/google/uuid"
)

// PVZ — пункт выдачи заказов.
type PVZ struct {
	ID               uuid.UUID
	RegistrationDate time.Time
	City             string
}

// PVZWithReceptions — ПВЗ с приёмками и их товарами: элемент списка ПВЗ.
type PVZWithReceptions struct {
	PVZ        PVZ
	Receptions []ReceptionWithProducts
}

// ReceptionWithProducts — приёмка с принятыми в ней товарами.
type ReceptionWithProducts struct {
	Reception Reception
	Products  []Product
}

// Period — период по дате приёмки, обе границы включительно. nil — граница
// не задана.
type Period struct {
	Start *time.Time
	End   *time.Time
}

// IsSet сообщает, что задана хотя бы одна граница.
func (p Period) IsSet() bool {
	return p.Start != nil || p.End != nil
}
