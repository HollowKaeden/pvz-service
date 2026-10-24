package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

func TestDummyLogin(t *testing.T) {
	errSign := errors.New("sign failed")

	tests := []struct {
		name      string
		role      domain.Role
		issueErr  error
		issues    bool // дойдёт ли до выдачи токена
		wantToken string
		wantErr   error
	}{
		{name: "сотрудник", role: domain.RoleEmployee, issues: true, wantToken: "token"},
		{name: "модератор", role: domain.RoleModerator, issues: true, wantToken: "token"},
		{name: "роль из текста задания", role: "client", wantErr: domain.ErrInvalidRole},
		{name: "пустая роль", role: "", wantErr: domain.ErrInvalidRole},
		{name: "ошибка подписи", role: domain.RoleEmployee, issues: true, issueErr: errSign, wantErr: errSign},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := newMockTokenIssuer(t)
			if tt.issues {
				token := "token"
				if tt.issueErr != nil {
					token = ""
				}
				tokens.EXPECT().Issue(tt.role).Return(token, tt.issueErr)
			}

			token, err := NewAuth(tokens).DummyLogin(context.Background(), tt.role)

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
