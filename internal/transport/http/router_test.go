package httptransport

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
