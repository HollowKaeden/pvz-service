package httptransport

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/api"
)

func TestHealth(t *testing.T) {
	h := NewRouter(slog.New(slog.DiscardHandler))

	tests := []struct {
		name       string
		method     string
		wantStatus int
	}{
		{name: "GET отвечает 200", method: http.MethodGet, wantStatus: http.StatusOK},
		{name: "POST не разрешён", method: http.MethodPost, wantStatus: http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, "/health", nil))

			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantStatus == http.StatusOK {
				assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
				assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
			}
		})
	}
}

// Все ручки контракта зарегистрированы и до реализации отвечают 501 с телом
// ошибки из контракта.
func TestContractRoutesNotImplemented(t *testing.T) {
	h := NewRouter(slog.New(slog.DiscardHandler))
	const pvzID = "6f1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"

	tests := []struct {
		method, path, body string
	}{
		{http.MethodPost, "/dummyLogin", `{"role":"employee"}`},
		{http.MethodPost, "/register", `{"email":"a@b.ru","password":"p","role":"employee"}`},
		{http.MethodPost, "/login", `{"email":"a@b.ru","password":"p"}`},
		{http.MethodPost, "/pvz", `{"city":"Москва"}`},
		{http.MethodGet, "/pvz?page=1&limit=10", ""},
		{http.MethodPost, "/receptions", `{"pvzId":"` + pvzID + `"}`},
		{http.MethodPost, "/pvz/" + pvzID + "/close_last_reception", ""},
		{http.MethodPost, "/products", `{"type":"обувь","pvzId":"` + pvzID + `"}`},
		{http.MethodPost, "/pvz/" + pvzID + "/delete_last_product", ""},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := serve(h, tt.method, tt.path, tt.body)

			assert.Equal(t, http.StatusNotImplemented, rec.Code)
			assertErrorBody(t, rec)
		})
	}
}

// Запрос, который не разбирается по контракту, получает 400 до вызова ручки.
func TestBadRequest(t *testing.T) {
	h := NewRouter(slog.New(slog.DiscardHandler))

	tests := []struct {
		name, method, path, body string
	}{
		{"невалидный JSON", http.MethodPost, "/pvz", `{"city":`},
		{"UUID в пути", http.MethodPost, "/pvz/not-a-uuid/close_last_reception", ""},
		{"UUID в теле", http.MethodPost, "/receptions", `{"pvzId":"42"}`},
		{"число в query", http.MethodGet, "/pvz?page=first", ""},
		{"дата в query", http.MethodGet, "/pvz?startDate=вчера", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(h, tt.method, tt.path, tt.body)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assertErrorBody(t, rec)
		})
	}
}

func TestDocs(t *testing.T) {
	h := NewRouter(slog.New(slog.DiscardHandler))

	t.Run("корень ведёт в Swagger UI", func(t *testing.T) {
		rec := serve(h, http.MethodGet, "/", "")
		assert.Equal(t, http.StatusFound, rec.Code)
		assert.Equal(t, "/docs/", rec.Header().Get("Location"))
	})
	t.Run("страница Swagger UI", func(t *testing.T) {
		rec := serve(h, http.MethodGet, "/docs/", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
		assert.Contains(t, rec.Body.String(), `url: "openapi.yaml"`)
	})
	t.Run("/docs без слеша", func(t *testing.T) {
		rec := serve(h, http.MethodGet, "/docs", "")
		// Редирект делает сам ServeMux; код зависит от версии Go (301 или 307).
		assert.Contains(t, []int{http.StatusMovedPermanently, http.StatusTemporaryRedirect}, rec.Code)
		assert.Equal(t, "/docs/", rec.Header().Get("Location"))
	})
	t.Run("спека отдаётся без изменений", func(t *testing.T) {
		rec := serve(h, http.MethodGet, "/docs/openapi.yaml", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, api.OpenAPI, rec.Body.Bytes())
	})
	t.Run("лишние пути под /docs/ не отдаются", func(t *testing.T) {
		rec := serve(h, http.MethodGet, "/docs/whatever", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func serve(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// assertErrorBody проверяет формат ошибки из контракта: {"message": "..."}.
func assertErrorBody(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var body struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	assert.NotEmpty(t, body.Message)
}

func TestLogRequests(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantStatus int
		wantBytes  int
	}{
		{
			name:       "статус по умолчанию 200",
			handler:    func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hello")) },
			wantStatus: http.StatusOK,
			wantBytes:  5,
		},
		{
			name: "явный статус",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
				_, _ = w.Write([]byte("{}"))
			},
			wantStatus: http.StatusTeapot,
			wantBytes:  2,
		},
		{
			name: "повторный WriteHeader не меняет статус",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantStatus: http.StatusCreated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&buf, nil))

			rec := httptest.NewRecorder()
			logRequests(log, tt.handler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/pvz?page=2", nil))

			var entry struct {
				Msg    string `json:"msg"`
				Method string `json:"method"`
				Path   string `json:"path"`
				Status int    `json:"status"`
				Bytes  int    `json:"bytes"`
				// Указатель: отличает «поля нет» от нулевой длительности.
				DurationMS *float64 `json:"duration_ms"`
			}
			require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
			assert.Equal(t, "http request", entry.Msg)
			assert.Equal(t, http.MethodGet, entry.Method)
			assert.Equal(t, "/pvz", entry.Path, "query не попадает в лог")
			assert.Equal(t, tt.wantStatus, entry.Status)
			assert.Equal(t, tt.wantBytes, entry.Bytes)
			assert.NotNil(t, entry.DurationMS)
			assert.Equal(t, tt.wantStatus, rec.Code, "статус доходит до клиента")
		})
	}
}
