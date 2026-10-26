package httptransport

import (
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// rateLimiter пропускает с одного IP не больше limit запросов за окно window
// (фиксированное окно: счётчик обнуляется, когда окно прошло). Обе ручки
// входа делят один счётчик. Состояние в памяти процесса: после рестарта
// счётчики обнуляются, для защиты от перебора этого хватает.
type rateLimiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu        sync.Mutex
	windows   map[string]ipWindow
	lastSweep time.Time
}

// ipWindow — запросы одного IP в текущем окне.
type ipWindow struct {
	start time.Time
	count int
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, now: time.Now, windows: map[string]ipWindow{}}
}

// allow учитывает запрос с ip. Если лимит исчерпан, возвращает false и
// сколько ждать до нового окна.
func (l *rateLimiter) allow(ip string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)
	w, ok := l.windows[ip]
	if !ok || now.Sub(w.start) >= l.window {
		w = ipWindow{start: now}
	}
	if w.count >= l.limit {
		return false, w.start.Add(l.window).Sub(now)
	}
	w.count++
	l.windows[ip] = w
	return true, 0
}

// sweep раз в окно удаляет закончившиеся окна, чтобы карта не росла от
// каждого адреса, который когда-то заходил.
func (l *rateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	for ip, w := range l.windows {
		if now.Sub(w.start) >= l.window {
			delete(l.windows, ip)
		}
	}
	l.lastSweep = now
}

// limitAuth отвечает 429, если с IP клиента пришло слишком много запросов к
// ручкам с пометкой limited в rules (вход и регистрация). limiter nil — лимит
// выключен. В контракте 429 нет, но по HTTP это именно он, а Retry-After
// подсказывает клиенту, когда повторить.
func limitAuth(limiter *rateLimiter, trusted []netip.Prefix, rules map[string]access) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if limiter == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !rules[r.Pattern].limited {
				next.ServeHTTP(w, r)
				return
			}
			ok, wait := limiter.allow(clientIP(r, trusted))
			if !ok {
				seconds := int(math.Ceil(wait.Seconds()))
				w.Header().Set("Retry-After", strconv.Itoa(seconds))
				writeError(w, http.StatusTooManyRequests,
					"слишком много попыток входа и регистрации с вашего адреса, повторите через "+strconv.Itoa(seconds)+" с")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
