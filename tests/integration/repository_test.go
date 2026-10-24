//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/repository/postgres"
)

// Уникальный индекс не даёт открыть вторую приёмку, даже если запись идёт в
// обход сервиса и его блокировки. Репозиторий отвечает той же доменной
// ошибкой, что и сервис.
func TestOpenReceptionUniqueIndex(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	pvzID := createPVZ(t, login(t, domain.RoleModerator), "Москва")
	repo := postgres.NewReceptionRepository(postgres.NewDB(pool))
	open := func() domain.Reception {
		return domain.Reception{ID: uuid.New(), PVZID: pvzID, DateTime: time.Now(), Status: domain.ReceptionInProgress}
	}

	require.NoError(t, repo.Create(ctx, open()))
	err := repo.Create(ctx, open())

	require.ErrorIs(t, err, domain.ErrReceptionAlreadyOpen)

	// Закрытые приёмки индекс не ограничивает.
	closed := open()
	closed.Status = domain.ReceptionClosed
	require.NoError(t, repo.Create(ctx, closed))
}

// Несуществующий ПВЗ во всех ручках с pvzId — 404.
func TestUnknownPVZ(t *testing.T) {
	resetDB(t)
	employee := login(t, domain.RoleEmployee)
	missing := uuid.New().String()

	tests := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/receptions", map[string]any{"pvzId": missing}},
		{http.MethodPost, "/pvz/" + missing + "/close_last_reception", nil},
		{http.MethodPost, "/products", map[string]any{"pvzId": missing, "type": "обувь"}},
		{http.MethodPost, "/pvz/" + missing + "/delete_last_product", nil},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			status, body := call(t, tt.method, tt.path, employee, tt.body)

			assert.Equal(t, http.StatusNotFound, status)
			assert.Contains(t, string(body), "ПВЗ не найден")
		})
	}
}

// /health проверяет настоящее соединение с базой.
func TestHealth(t *testing.T) {
	status, body := call(t, http.MethodGet, "/health", "", nil)

	assert.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}
