package domain

import "github.com/google/uuid"

// User — зарегистрированный пользователь. Пароль не хранится: только его
// bcrypt-хеш.
type User struct {
	ID           uuid.UUID
	Email        string
	Role         Role
	PasswordHash []byte
}
