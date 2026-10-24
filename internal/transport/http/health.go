package httptransport

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Pinger проверяет, что база данных отвечает. Его реализует *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

// healthTimeout — сколько ждать ответа БД. Healthcheck не должен висеть
// дольше, чем его ждёт docker.
const healthTimeout = 2 * time.Second

// health — проверка для healthcheck'а compose и проверки после деплоя:
// 200, если Postgres отвечает, иначе 503.
func health(db Pinger, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
		defer cancel()

		w.Header().Set("Content-Type", "application/json")
		if err := db.Ping(ctx); err != nil {
			log.WarnContext(ctx, "health: БД не отвечает", slog.Any("error", err))
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"unavailable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}
