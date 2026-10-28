package httptransport

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/service"
)

// observed — один учтённый запрос.
type observed struct {
	route  string
	status int
}

// metricsSpy — RequestMetrics для тестов: запоминает учтённые запросы.
type metricsSpy struct{ calls []observed }

func (s *metricsSpy) ObserveHTTP(route string, status int, _ time.Duration) {
	s.calls = append(s.calls, observed{route: route, status: status})
}

// В метрики попадает шаблон маршрута, а не путь: id ПВЗ в пути не плодит
// новых рядов. Запросы мимо всех маршрутов идут под одним общим именем.
func TestMeasureRequests(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		role   domain.Role // пусто — без токена
		want   observed
	}{
		{
			name: "ручка контракта", method: http.MethodPost, path: "/pvz", body: `{"city":"Москва"}`,
			role: domain.RoleModerator,
			want: observed{route: "POST /pvz", status: http.StatusCreated},
		},
		{
			name: "id в пути не попадает в метрику", method: http.MethodPost,
			path: "/pvz/0192f1a4-7b3c-7d2e-8f00-000000000001/delete_last_product",
			want: observed{route: "POST /pvz/{pvzId}/delete_last_product", status: http.StatusUnauthorized},
		},
		{
			name: "служебная ручка", method: http.MethodGet, path: "/health",
			want: observed{route: "GET /health", status: http.StatusOK},
		},
		{
			name: "файл фронтенда", method: http.MethodGet, path: "/assets/app.js",
			want: observed{route: "GET /assets/", status: http.StatusOK},
		},
		{
			name: "нет такого пути", method: http.MethodGet, path: "/wp-login.php",
			want: observed{route: unmatchedRoute, status: http.StatusNotFound},
		},
		{
			name: "нет такого метода", method: http.MethodDelete, path: "/health",
			want: observed{route: unmatchedRoute, status: http.StatusMethodNotAllowed},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spy := &metricsSpy{}
			h := NewRouter(slog.New(slog.DiscardHandler), Deps{
				Metrics:    spy,
				DB:         fakeDB{},
				Tokens:     testTokens,
				Auth:       fakeAuth{service.NewAuth(testTokens, nil)},
				PVZ:        fakePVZ{},
				Receptions: fakeReceptions{},
				Products:   fakeProducts{},
			})
			r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.body != "" {
				r.Header.Set("Content-Type", "application/json")
			}
			if tt.role != "" {
				r.Header.Set("Authorization", "Bearer "+tokenFor(t, tt.role))
			}
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, r)

			require.Equal(t, tt.want.status, rec.Code)
			assert.Equal(t, []observed{tt.want}, spy.calls)
		})
	}
}
