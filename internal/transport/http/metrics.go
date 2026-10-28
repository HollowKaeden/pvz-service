package httptransport

import (
	"net/http"
	"time"
)

// RequestMetrics учитывает обработанные HTTP-запросы.
type RequestMetrics interface {
	ObserveHTTP(route string, status int, d time.Duration)
}

// unmatchedRoute — маршрут в метриках для запросов, которые не подошли ни к
// одному маршруту (404 и 405 от ServeMux). Сырой путь туда не пишется: иначе
// каждый путь от сканера уязвимостей завёл бы свой ряд метрик.
const unmatchedRoute = "unmatched"

// measureRequests учитывает каждый запрос: маршрут, код ответа и время
// обработки. Маршрут — шаблон из ServeMux (например,
// "POST /pvz/{pvzId}/close_last_reception"), а не путь запроса.
func measureRequests(m RequestMetrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		// ServeMux записывает шаблон найденного маршрута в тот же *Request,
		// который получил, поэтому после обработки он виден и здесь.
		route := r.Pattern
		if route == "" {
			route = unmatchedRoute
		}
		m.ObserveHTTP(route, rec.status, time.Since(start))
	})
}
