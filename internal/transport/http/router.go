// Package httptransport — HTTP-слой pvz: маршруты, хендлеры и middleware.
// Здесь только приём и отдача запросов, бизнес-правила живут в service.
package httptransport

import (
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/HollowKaeden/pvz-service/internal/transport/http/oapi"
)

// maxBodyBytes — наибольший размер тела запроса. Самое большое тело в API —
// пара полей JSON, так что 1 МБ с большим запасом.
const maxBodyBytes = 1 << 20

// Deps — зависимости HTTP-слоя.
type Deps struct {
	// TrustedProxies — прокси, которым можно верить в X-Forwarded-For.
	TrustedProxies []netip.Prefix

	DB         Pinger
	Tokens     TokenParser
	Auth       AuthService
	PVZ        PVZService
	Receptions ReceptionService
	Products   ProductService
}

// NewRouter собирает все HTTP-маршруты сервиса: ручки контракта и служебные
// (health, Swagger UI). Ручки контракта закрыты проверкой токена и роли.
func NewRouter(log *slog.Logger, deps Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health(deps.DB, log))
	// Пока фронта нет, корень поддомена ведёт в Swagger UI. 302, а не 301:
	// когда появится фронт, браузеры не должны помнить старый редирект.
	mux.Handle("GET /{$}", http.RedirectHandler("/docs/", http.StatusFound))
	mux.HandleFunc("GET /docs/{$}", swaggerUI)
	mux.HandleFunc("GET /docs/openapi.yaml", openAPISpec)

	strict := oapi.NewStrictHandlerWithOptions(API{auth: deps.Auth, pvz: deps.PVZ, receptions: deps.Receptions, products: deps.Products}, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: handlerError(log),
	})
	oapi.HandlerWithOptions(strict, oapi.StdHTTPServerOptions{
		BaseRouter:       mux,
		ErrorHandlerFunc: badRequest,
		Middlewares:      []oapi.MiddlewareFunc{authorize(deps.Tokens, routeAccess())},
	})

	return logRequests(log, deps.TrustedProxies, limitBody(mux))
}

// limitBody ограничивает тело запроса maxBodyBytes: чтение сверх лимита
// возвращает ошибку, и сервис отвечает 413, не дочитывая тело.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}
