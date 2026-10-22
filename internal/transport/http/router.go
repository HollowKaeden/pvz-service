// Package httptransport — HTTP-слой pvz: маршруты, хендлеры и middleware.
// Здесь только приём и отдача запросов, бизнес-правила живут в service.
package httptransport

import (
	"log/slog"
	"net/http"
)

// NewRouter собирает все HTTP-маршруты сервиса.
func NewRouter(log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)

	return logRequests(log, mux)
}

// health — проверка живости для healthcheck'а compose и проверки после деплоя.
func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
