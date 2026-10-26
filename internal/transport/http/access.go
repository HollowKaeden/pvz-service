package httptransport

import (
	"net/http"
	"slices"
	"strings"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// TokenParser проверяет токен доступа и возвращает роль из него.
type TokenParser interface {
	Parse(token string) (domain.Role, error)
}

// access — кто может вызвать ручку.
type access struct {
	public bool          // без токена
	roles  []domain.Role // с токеном одной из этих ролей
	// limited — ручка под лимитом запросов с одного IP (limitAuth): на ней
	// подбирают пароли и заводят пачки пользователей. Остальные ручки лимит
	// не трогает, иначе он мешал бы нагрузочному тесту.
	limited bool
}

// routeAccess — права на ручки контракта. Ключ — шаблон маршрута, под которым
// его регистрирует сгенерированный роутер (http.Request.Pattern). Ручка,
// которой нет в таблице, закрыта для всех: забытая строка не откроет её без
// токена. Что таблица совпадает с security в контракте, проверяет тест.
func routeAccess() map[string]access {
	employee := []domain.Role{domain.RoleEmployee}
	return map[string]access{
		"POST /dummyLogin":                       {public: true},
		"POST /register":                         {public: true, limited: true},
		"POST /login":                            {public: true, limited: true},
		"POST /pvz":                              {roles: []domain.Role{domain.RoleModerator}},
		"GET /pvz":                               {roles: []domain.Role{domain.RoleEmployee, domain.RoleModerator}},
		"POST /receptions":                       {roles: employee},
		"POST /pvz/{pvzId}/close_last_reception": {roles: employee},
		"POST /products":                         {roles: employee},
		"POST /pvz/{pvzId}/delete_last_product":  {roles: employee},
	}
}

// authorize пускает к ручке только тех, кому она доступна по rules.
// Срабатывает до разбора тела: без прав клиент получает отказ, даже если тело
// запроса некорректное.
//
// Коды по HTTP: 401 — нет токена или он недействителен (кто вы, неизвестно),
// 403 — токен верный, но роли это действие не разрешено.
func authorize(tokens TokenParser, rules map[string]access) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rule, ok := rules[r.Pattern]
			if !ok {
				writeError(w, http.StatusForbidden, "доступ запрещён")
				return
			}
			if rule.public {
				next.ServeHTTP(w, r)
				return
			}

			token, ok := bearerToken(r)
			if !ok {
				unauthorized(w, "нужен токен: заголовок Authorization: Bearer <токен из /dummyLogin>")
				return
			}
			role, err := tokens.Parse(token)
			if err != nil {
				unauthorized(w, "токен недействителен")
				return
			}
			if !slices.Contains(rule.roles, role) {
				writeError(w, http.StatusForbidden, "недостаточно прав: нужна роль "+joinRoles(rule.roles))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// unauthorized отвечает 401. Заголовок WWW-Authenticate обязателен для 401
// и подсказывает клиенту схему входа (RFC 6750).
func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, http.StatusUnauthorized, msg)
}

// bearerToken достаёт токен из заголовка «Authorization: Bearer <токен>».
// Название схемы по RFC 6750 регистронезависимо.
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func joinRoles(roles []domain.Role) string {
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = string(r)
	}
	return strings.Join(names, " или ")
}
