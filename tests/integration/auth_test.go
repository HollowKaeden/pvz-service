//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/repository/postgres"
	"github.com/HollowKaeden/pvz-service/internal/transport/http/oapi"
)

// Регистрация и вход по email: токен из /login работает так же, как токен из
// /dummyLogin, с ролью, выбранной при регистрации.
func TestRegisterAndLogin(t *testing.T) {
	resetDB(t)

	var user oapi.User
	mustCall(t, http.StatusCreated, http.MethodPost, "/register", "",
		map[string]string{"email": "Ivan@Example.com", "password": "password1", "role": "moderator"}, &user)
	require.NotNil(t, user.ID)
	assert.Equal(t, "ivan@example.com", string(user.Email), "email хранится в нижнем регистре")
	assert.Equal(t, oapi.UserRoleModerator, user.Role)

	// Пароль в базе только хешем.
	var hash string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT password_hash FROM users WHERE id = $1`, *user.ID).Scan(&hash))
	assert.NotContains(t, hash, "password1")

	var token string
	mustCall(t, http.StatusOK, http.MethodPost, "/login", "",
		map[string]string{"email": "IVAN@example.com", "password": "password1"}, &token)
	createPVZ(t, token, "Москва") // модератору можно заводить ПВЗ

	t.Run("email уже занят, в другом регистре тоже", func(t *testing.T) {
		status, body := call(t, http.MethodPost, "/register", "",
			map[string]string{"email": "ivan@EXAMPLE.com", "password": "password2", "role": "employee"})
		assert.Equal(t, http.StatusBadRequest, status)
		assert.Contains(t, string(body), "пользователь с таким email уже зарегистрирован")
	})
	t.Run("неверный пароль и неизвестный email — один ответ", func(t *testing.T) {
		for _, email := range []string{"ivan@example.com", "nobody@example.com"} {
			status, body := call(t, http.MethodPost, "/login", "",
				map[string]string{"email": email, "password": "wrong-password"})
			assert.Equal(t, http.StatusUnauthorized, status, email)
			assert.JSONEq(t, `{"message":"неверный email или пароль"}`, string(body), email)
		}
	})
	t.Run("сброс демо удаляет пользователей", func(t *testing.T) {
		require.NoError(t, postgres.ClearData(context.Background(), pool))
		status, _ := call(t, http.MethodPost, "/login", "",
			map[string]string{"email": "ivan@example.com", "password": "password1"})
		assert.Equal(t, http.StatusUnauthorized, status)
	})
}

// Одновременные регистрации с одним email: успешна ровно одна. Отдельной
// проверки «email свободен» перед вставкой нет, занятость ловит уникальность
// в базе.
func TestRegisterRace(t *testing.T) {
	resetDB(t)

	const n = 10
	statuses := parallel(t, repeat(n, request{http.MethodPost, "/register", "",
		map[string]string{"email": "race@example.com", "password": "password1", "role": "employee"}}))

	assert.Equal(t, map[int]int{http.StatusCreated: 1, http.StatusBadRequest: n - 1}, countStatuses(statuses))
}
