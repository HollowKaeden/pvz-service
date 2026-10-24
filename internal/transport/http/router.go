// Package httptransport — HTTP-слой pvz: маршруты, хендлеры и middleware.
// Здесь только приём и отдача запросов, бизнес-правила живут в service.
package httptransport

import (
	"log/slog"
	"net/http"

	"github.com/HollowKaeden/pvz-service/internal/transport/http/oapi"
)

// Deps — зависимости HTTP-слоя.
type Deps struct {
	DB     Pinger
	Tokens TokenParser
	Auth   AuthService
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

	strict := oapi.NewStrictHandlerWithOptions(API{auth: deps.Auth}, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: handlerError(log),
	})
	oapi.HandlerWithOptions(strict, oapi.StdHTTPServerOptions{
		BaseRouter:       mux,
		ErrorHandlerFunc: badRequest,
		Middlewares:      []oapi.MiddlewareFunc{authorize(deps.Tokens, routeAccess())},
	})

	return logRequests(log, mux)
}
