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
