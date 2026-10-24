package service

import (
	"context"
	"fmt"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// TokenIssuer выдаёт токен доступа для роли.
type TokenIssuer interface {
	Issue(role domain.Role) (string, error)
}

// Auth — вход в систему.
type Auth struct {
	tokens TokenIssuer
}

// NewAuth создаёт сервис входа.
func NewAuth(tokens TokenIssuer) *Auth {
	return &Auth{tokens: tokens}
}

// DummyLogin выдаёт тестовый токен с выбранной ролью, без пользователя и
// пароля (ручка /dummyLogin из задания).
func (a *Auth) DummyLogin(_ context.Context, role domain.Role) (string, error) {
	if !role.Valid() {
		return "", fmt.Errorf("%w %q: доступны %s и %s",
			domain.ErrInvalidRole, role, domain.RoleEmployee, domain.RoleModerator)
	}
	token, err := a.tokens.Issue(role)
	if err != nil {
		return "", fmt.Errorf("dummy login: %w", err)
	}
	return token, nil
}
