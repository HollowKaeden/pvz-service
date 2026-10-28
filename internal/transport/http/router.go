// Package httptransport — HTTP-слой pvz: маршруты, хендлеры и middleware.
// Здесь только приём и отдача запросов, бизнес-правила живут в service.
package httptransport

import (
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/HollowKaeden/pvz-service/internal/transport/http/oapi"
)

// maxBodyBytes — наибольший размер тела запроса. Самое большое тело в API —
// пара полей JSON, так что 1 МБ с большим запасом.
const maxBodyBytes = 1 << 20

// Deps — зависимости HTTP-слоя.
type Deps struct {
	// TrustedProxies — прокси, которым можно верить в X-Forwarded-For.
	TrustedProxies []netip.Prefix

	// AuthLimit — сколько запросов к /register и /login пропускать с одного
	// IP за AuthWindow. 0 — лимит выключен.
	AuthLimit  int
	AuthWindow time.Duration

	// Metrics учитывает запросы для Prometheus. nil — без метрик.
	Metrics RequestMetrics

	DB         Pinger
	Tokens     TokenParser
	Auth       AuthService
	PVZ        PVZService
	Receptions ReceptionService
	Products   ProductService
}

// NewRouter собирает все HTTP-маршруты сервиса: ручки контракта, служебные
// (health, Swagger UI) и фронтенд. Ручки контракта закрыты проверкой токена и
// роли.
func NewRouter(log *slog.Logger, deps Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health(deps.DB, log))
	// Фронтенд: страница на / и её файлы на /assets/ (web/).
	mux.HandleFunc("GET /{$}", frontendPage)
	mux.Handle("GET /assets/", frontendAssets())
	mux.HandleFunc("GET /docs/{$}", swaggerUI)
	mux.HandleFunc("GET /docs/openapi.yaml", openAPISpec)

	var limiter *rateLimiter
	if deps.AuthLimit > 0 {
		limiter = newRateLimiter(deps.AuthLimit, deps.AuthWindow)
	}
	rules := routeAccess()

	strict := oapi.NewStrictHandlerWithOptions(API{auth: deps.Auth, pvz: deps.PVZ, receptions: deps.Receptions, products: deps.Products}, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: handlerError(log),
	})
	oapi.HandlerWithOptions(strict, oapi.StdHTTPServerOptions{
		BaseRouter:       mux,
		ErrorHandlerFunc: badRequest,
		Middlewares: []oapi.MiddlewareFunc{
			authorize(deps.Tokens, rules),
			limitAuth(limiter, deps.TrustedProxies, rules),
		},
	})

	h := limitBody(mux)
	if deps.Metrics != nil {
		h = measureRequests(deps.Metrics, h)
	}
	return logRequests(log, deps.TrustedProxies, h)
}

// limitBody ограничивает тело запроса maxBodyBytes: чтение сверх лимита
// возвращает ошибку, и сервис отвечает 413, не дочитывая тело.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}
