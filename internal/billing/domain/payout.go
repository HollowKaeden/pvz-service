// Package domain содержит модель начислений, независимую от сервиса приёмки.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrPermanent означает непригодное событие; повтор не исправит его данные.
var ErrPermanent = errors.New("некорректное событие")

// Rate — ставка за один товар в целых копейках.
type Rate struct {
	ProductType string
	Amount      int64
}

// Payout — одно начисление за закрытую приёмку.
type Payout struct {
	EventID, ReceptionID, PVZID uuid.UUID
	City                        string
	Amount                      int64
	Products                    int
	ClosedAt, ProcessedAt       time.Time
}

// Balance — сумма всех начислений одного ПВЗ.
type Balance struct {
	PVZID     uuid.UUID
	City      string
	Amount    int64
	UpdatedAt time.Time
}
