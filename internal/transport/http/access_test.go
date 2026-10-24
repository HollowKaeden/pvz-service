package httptransport

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/HollowKaeden/pvz-service/api"
	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// fakeParser принимает только токены из своей таблицы.
type fakeParser map[string]domain.Role

func (f fakeParser) Parse(token string) (domain.Role, error) {
	role, ok := f[token]
	if !ok {
		return "", errors.New("unknown token")
	}
	return role, nil
}

func TestAuthorize(t *testing.T) {
	rules := map[string]access{
		"GET /open":   {public: true},
		"GET /mod":    {roles: []domain.Role{domain.RoleModerator}},
		"GET /staff":  {roles: []domain.Role{domain.RoleEmployee, domain.RoleModerator}},
		"GET /nobody": {roles: nil},
	}
	tokens := fakeParser{"emp": domain.RoleEmployee, "mod": domain.RoleModerator}

	// Маршруты регистрируются в ServeMux, как в настоящем роутере: middleware
	// узнаёт ручку по r.Pattern, который выставляет ServeMux.
	mux := http.NewServeMux()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := authorize(tokens, rules)
	for _, pattern := range []string{"GET /open", "GET /mod", "GET /staff", "GET /nobody", "GET /unlisted"} {
		mux.Handle(pattern, mw(ok))
	}

	tests := []struct {
		name       string
		path       string
		authHeader string
		wantStatus int
		wantMsg    string
	}{
		{name: "открытая ручка без токена", path: "/open", wantStatus: http.StatusOK},
		// Заодно проверяется, что угловые скобки в тексте не экранируются.
		{name: "нет заголовка", path: "/mod", wantStatus: http.StatusUnauthorized, wantMsg: "Bearer <токен из /dummyLogin>"},
		{name: "другая схема", path: "/mod", authHeader: "Basic bW9kOg==", wantStatus: http.StatusUnauthorized, wantMsg: "нужен токен"},
		{name: "Bearer без токена", path: "/mod", authHeader: "Bearer ", wantStatus: http.StatusUnauthorized, wantMsg: "нужен токен"},
		{name: "токен без схемы", path: "/mod", authHeader: "mod", wantStatus: http.StatusUnauthorized, wantMsg: "нужен токен"},
		{name: "недействительный токен", path: "/mod", authHeader: "Bearer forged", wantStatus: http.StatusUnauthorized, wantMsg: "токен недействителен"},
		{name: "чужая роль", path: "/mod", authHeader: "Bearer emp", wantStatus: http.StatusForbidden, wantMsg: "нужна роль moderator"},
		{name: "своя роль", path: "/mod", authHeader: "Bearer mod", wantStatus: http.StatusOK},
		{name: "схема в другом регистре", path: "/mod", authHeader: "bearer mod", wantStatus: http.StatusOK},
		{name: "одна из ролей", path: "/staff", authHeader: "Bearer emp", wantStatus: http.StatusOK},
		{name: "ручка без ролей", path: "/nobody", authHeader: "Bearer mod", wantStatus: http.StatusForbidden},
		{name: "ручки нет в таблице", path: "/unlisted", authHeader: "Bearer mod", wantStatus: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantStatus == http.StatusOK {
				return
			}
			assertErrorBody(t, rec)
			assert.Contains(t, rec.Body.String(), tt.wantMsg)
			if tt.wantStatus == http.StatusUnauthorized {
				assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
			} else {
				assert.Empty(t, rec.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

// Таблица доступа описывает ровно ручки контракта, и открыты в ней ровно те,
// у которых в контракте нет security.
func TestRouteAccessMatchesContract(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]struct {
			Security []map[string][]string `yaml:"security"`
		} `yaml:"paths"`
	}
	require.NoError(t, yaml.Unmarshal(api.OpenAPI, &spec))

	rules := routeAccess()
	contract := 0
	for path, ops := range spec.Paths {
		for method, op := range ops {
			contract++
			pattern := strings.ToUpper(method) + " " + path
			rule, ok := rules[pattern]
			if !assert.True(t, ok, "нет правила для %s", pattern) {
				continue
			}
			assert.Equal(t, len(op.Security) == 0, rule.public, "public у %s не совпадает с контрактом", pattern)
			if !rule.public {
				assert.NotEmpty(t, rule.roles, "у %s не указаны роли", pattern)
			}
		}
	}
	assert.Len(t, rules, contract, "в таблице есть ручки, которых нет в контракте")
}
