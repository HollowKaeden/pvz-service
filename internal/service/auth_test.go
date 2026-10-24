package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// fakeTokens выдаёт токен вида "token-<роль>" или заданную ошибку.
type fakeTokens struct{ err error }

func (f fakeTokens) Issue(role domain.Role) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "token-" + string(role), nil
}

func TestDummyLogin(t *testing.T) {
	errSign := errors.New("sign failed")

	tests := []struct {
		name      string
		role      domain.Role
		tokens    fakeTokens
		wantToken string
		wantErr   error
	}{
		{name: "сотрудник", role: domain.RoleEmployee, wantToken: "token-employee"},
		{name: "модератор", role: domain.RoleModerator, wantToken: "token-moderator"},
		{name: "роль из текста задания", role: "client", wantErr: domain.ErrInvalidRole},
		{name: "пустая роль", role: "", wantErr: domain.ErrInvalidRole},
		{name: "ошибка подписи", role: domain.RoleEmployee, tokens: fakeTokens{err: errSign}, wantErr: errSign},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := NewAuth(tt.tokens).DummyLogin(context.Background(), tt.role)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Empty(t, token)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantToken, token)
		})
	}
}
