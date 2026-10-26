package httptransport

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/service"
)

func TestRateLimiter(t *testing.T) {
	start := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	now := start
	l := newRateLimiter(2, time.Minute)
	l.now = func() time.Time { return now }

	ok, _ := l.allow("192.0.2.1")
	assert.True(t, ok)
	ok, _ = l.allow("192.0.2.1")
	assert.True(t, ok)

	now = start.Add(20 * time.Second)
	ok, wait := l.allow("192.0.2.1")
	assert.False(t, ok, "третий запрос в окне")
	assert.Equal(t, 40*time.Second, wait, "до конца окна")

	ok, _ = l.allow("192.0.2.2")
	assert.True(t, ok, "у другого адреса свой счётчик")

	now = start.Add(time.Minute)
	ok, _ = l.allow("192.0.2.1")
	assert.True(t, ok, "окно прошло, счётчик обнулился")

	// Через окно после последнего запроса закончившиеся окна удаляются.
	now = start.Add(3 * time.Minute)
	ok, _ = l.allow("192.0.2.3")
	assert.True(t, ok)
	assert.Equal(t, []string{"192.0.2.3"}, mapKeys(l.windows))
}

func mapKeys(m map[string]ipWindow) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// Лимит действует на /register и /login вместе и только на них. IP клиента
// берётся из X-Forwarded-For, только если запрос пришёл от доверенного прокси.
func TestLimitAuth(t *testing.T) {
	proxy := netip.MustParsePrefix("172.30.0.2/32")
	h := NewRouter(slog.New(slog.DiscardHandler), Deps{
		TrustedProxies: []netip.Prefix{proxy},
		AuthLimit:      2,
		AuthWindow:     time.Minute,
		DB:             fakeDB{},
		Tokens:         testTokens,
		Auth:           fakeAuth{service.NewAuth(testTokens, nil)},
		PVZ:            fakePVZ{},
		Receptions:     fakeReceptions{},
		Products:       fakeProducts{},
	})
	loginBody := `{"email":"` + emailUser + `","password":"` + userPassword + `"}`
	registerBody := `{"email":"ivan@example.com","password":"password1","role":"employee"}`
	from := func(path, body, remoteAddr, xff string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = remoteAddr
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	assert.Equal(t, http.StatusOK, from("/login", loginBody, "198.51.100.7:5000", "").Code)
	assert.Equal(t, http.StatusCreated, from("/register", registerBody, "198.51.100.7:5000", "").Code)

	rec := from("/login", loginBody, "198.51.100.7:5000", "")
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	assertErrorBody(t, rec)
	assert.Equal(t, "60", rec.Header().Get("Retry-After"))
	assert.Contains(t, rec.Body.String(), "повторите через 60 с")

	// /dummyLogin под лимит не попадает.
	assert.Equal(t, http.StatusOK, from("/dummyLogin", `{"role":"employee"}`, "198.51.100.7:5000", "").Code)

	// Через прокси: считается адрес клиента из заголовка, а не адрес прокси.
	assert.Equal(t, http.StatusOK, from("/login", loginBody, "172.30.0.2:4000", "203.0.113.9").Code)
	assert.Equal(t, http.StatusOK, from("/login", loginBody, "172.30.0.2:4000", "203.0.113.10").Code)
	assert.Equal(t, http.StatusOK, from("/login", loginBody, "172.30.0.2:4000", "203.0.113.9").Code)
	assert.Equal(t, http.StatusTooManyRequests, from("/login", loginBody, "172.30.0.2:4000", "203.0.113.9").Code)

	// Не от прокси: подставленный заголовок не помогает обойти лимит.
	assert.Equal(t, http.StatusTooManyRequests, from("/login", loginBody, "198.51.100.7:5000", "203.0.113.50").Code)
}

// Без AUTH_RATE_LIMIT лимита нет.
func TestLimitAuthDisabled(t *testing.T) {
	h := newTestRouter()
	for range 20 {
		rec := serve(h, http.MethodPost, "/login", `{"email":"`+emailUser+`","password":"`+userPassword+`"}`, "")
		require.Equal(t, http.StatusOK, rec.Code)
	}
}
