package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

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

			token, err := NewAuth(tokens, newMockUserRepository(t)).DummyLogin(context.Background(), tt.role)

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

// newTestAuth — сервис входа с минимальной стоимостью bcrypt (тесты не ждут
// полноценного хеша) и предсказуемым id нового пользователя.
func newTestAuth(t *testing.T, tokens TokenIssuer, users UserRepository) *Auth {
	t.Helper()
	a := NewAuth(tokens, users)
	a.hashCost = bcrypt.MinCost
	a.newID = func() (uuid.UUID, error) { return testUserID, nil }
	return a
}

var testUserID = uuid.MustParse("0192f1a4-7b3c-7d2e-8f00-0000000000c1")

func TestRegister(t *testing.T) {
	errDB := errors.New("connection reset")

	tests := []struct {
		name      string
		email     string
		password  string
		role      domain.Role
		creates   bool // дойдёт ли до записи в базу
		createErr error
		wantEmail string
		wantErr   error
		wantMsg   string
	}{
		{
			name:  "сотрудник",
			email: "ivan@example.com", password: "password1", role: domain.RoleEmployee,
			creates: true, wantEmail: "ivan@example.com",
		},
		{
			name:  "email приводится к нижнему регистру",
			email: "  Ivan@Example.COM ", password: "password1", role: domain.RoleModerator,
			creates: true, wantEmail: "ivan@example.com",
		},
		{
			name:  "8 кириллических букв",
			email: "ivan@example.com", password: "парольпа", role: domain.RoleEmployee,
			creates: true, wantEmail: "ivan@example.com",
		},
		{
			name:  "ровно 72 байта",
			email: "ivan@example.com", password: strings.Repeat("a", 72), role: domain.RoleEmployee,
			creates: true, wantEmail: "ivan@example.com",
		},
		{
			name:  "роль из текста задания",
			email: "ivan@example.com", password: "password1", role: "client",
			wantErr: domain.ErrInvalidRole,
		},
		{
			name:  "короткий пароль",
			email: "ivan@example.com", password: "passwor", role: domain.RoleEmployee,
			wantErr: domain.ErrInvalidPassword, wantMsg: "недопустимый пароль: нужно не меньше 8 символов",
		},
		{
			name:  "длиннее 72 байт",
			email: "ivan@example.com", password: strings.Repeat("я", 37), role: domain.RoleEmployee,
			wantErr: domain.ErrInvalidPassword,
		},
		{
			name:  "email занят",
			email: "ivan@example.com", password: "password1", role: domain.RoleEmployee,
			creates: true, createErr: domain.ErrEmailTaken,
			wantErr: domain.ErrEmailTaken, wantMsg: "пользователь с таким email уже зарегистрирован",
		},
		{
			name:  "ошибка БД",
			email: "ivan@example.com", password: "password1", role: domain.RoleEmployee,
			creates: true, createErr: errDB, wantErr: errDB,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := newMockUserRepository(t)
			var saved domain.User
			if tt.creates {
				users.EXPECT().Create(mock.Anything, mock.Anything).
					Run(func(_ context.Context, u domain.User) { saved = u }).
					Return(tt.createErr)
			}

			user, err := newTestAuth(t, newMockTokenIssuer(t), users).
				Register(context.Background(), tt.email, tt.password, tt.role)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				if tt.wantMsg != "" {
					assert.EqualError(t, err, tt.wantMsg)
				}
				assert.Zero(t, user)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, saved, user)
			assert.Equal(t, testUserID, user.ID)
			assert.Equal(t, tt.wantEmail, user.Email)
			assert.Equal(t, tt.role, user.Role)
			// В базу уходит хеш, а не пароль, и он подходит к паролю.
			assert.NotContains(t, string(user.PasswordHash), tt.password)
			assert.NoError(t, bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(tt.password)))
		})
	}
}

func TestLogin(t *testing.T) {
	errDB := errors.New("connection reset")
	errSign := errors.New("sign failed")
	hash, err := bcrypt.GenerateFromPassword([]byte("password1"), bcrypt.MinCost)
	require.NoError(t, err)
	user := domain.User{ID: testUserID, Email: "ivan@example.com", Role: domain.RoleModerator, PasswordHash: hash}

	tests := []struct {
		name      string
		email     string
		password  string
		findErr   error
		issues    bool // дойдёт ли до выдачи токена
		issueErr  error
		wantToken string
		wantErr   error
	}{
		{name: "верный пароль", email: "ivan@example.com", password: "password1", issues: true, wantToken: "token"},
		{name: "email в другом регистре", email: "IVAN@example.com", password: "password1", issues: true, wantToken: "token"},
		{name: "неверный пароль", email: "ivan@example.com", password: "password2", wantErr: domain.ErrInvalidCredentials},
		{
			// Тот же ответ, что и на неверный пароль: по нему не узнать, кто
			// зарегистрирован.
			name: "нет такого email", email: "ivan@example.com", password: "password1",
			findErr: domain.ErrUserNotFound, wantErr: domain.ErrInvalidCredentials,
		},
		{name: "ошибка БД", email: "ivan@example.com", password: "password1", findErr: errDB, wantErr: errDB},
		{
			name: "ошибка подписи", email: "ivan@example.com", password: "password1",
			issues: true, issueErr: errSign, wantErr: errSign,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := newMockUserRepository(t)
			found := user
			if tt.findErr != nil {
				found = domain.User{}
			}
			users.EXPECT().ByEmail(mock.Anything, "ivan@example.com").Return(found, tt.findErr)
			tokens := newMockTokenIssuer(t)
			if tt.issues {
				token := "token"
				if tt.issueErr != nil {
					token = ""
				}
				tokens.EXPECT().Issue(domain.RoleModerator).Return(token, tt.issueErr)
			}

			token, err := newTestAuth(t, tokens, users).Login(context.Background(), tt.email, tt.password)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				if errors.Is(tt.wantErr, domain.ErrInvalidCredentials) {
					assert.EqualError(t, err, "неверный email или пароль")
				}
				assert.Empty(t, token)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantToken, token)
		})
	}
}
