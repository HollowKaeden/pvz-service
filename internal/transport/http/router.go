// Package httptransport — HTTP-слой pvz: маршруты, хендлеры и middleware.
// Здесь только приём и отдача запросов, бизнес-правила живут в service.
package httptransport

import (
	"log/slog"
	"net/http"

	"github.com/HollowKaeden/pvz-service/internal/transport/http/oapi"
)

// NewRouter собирает все HTTP-маршруты сервиса: ручки контракта и служебные
// (health, Swagger UI).
func NewRouter(log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	// Пока фронта нет, корень поддомена ведёт в Swagger UI. 302, а не 301:
	// когда появится фронт, браузеры не должны помнить старый редирект.
	mux.Handle("GET /{$}", http.RedirectHandler("/docs/", http.StatusFound))
	mux.HandleFunc("GET /docs/{$}", swaggerUI)
	mux.HandleFunc("GET /docs/openapi.yaml", openAPISpec)

	strict := oapi.NewStrictHandlerWithOptions(API{}, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: handlerError(log),
	})
	oapi.HandlerWithOptions(strict, oapi.StdHTTPServerOptions{
		BaseRouter:       mux,
		ErrorHandlerFunc: badRequest,
	})

	return logRequests(log, mux)
}

// health — проверка живости для healthcheck'а compose и проверки после деплоя.
func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
