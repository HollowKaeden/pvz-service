package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecorder(t *testing.T) {
	r := New()

	r.ObserveHTTP("POST /receptions", http.StatusCreated, 30*time.Millisecond)
	r.ObserveHTTP("POST /receptions", http.StatusCreated, 300*time.Millisecond)
	r.ObserveGRPC("/pvz.v1.PVZService/GetPVZList", "OK", time.Millisecond)
	r.PVZCreated("Москва")
	r.PVZCreated("Москва")
	r.PVZCreated("Казань")
	r.ReceptionOpened()
	r.ReceptionClosed()
	r.ProductAdded("обувь")

	assert.InDelta(t, 2, testutil.ToFloat64(r.pvzCreated.WithLabelValues("Москва")), 0)
	assert.InDelta(t, 1, testutil.ToFloat64(r.pvzCreated.WithLabelValues("Казань")), 0)
	assert.InDelta(t, 1, testutil.ToFloat64(r.receptionsOpened), 0)
	assert.InDelta(t, 1, testutil.ToFloat64(r.receptionsClosed), 0)
	assert.InDelta(t, 1, testutil.ToFloat64(r.productsAdded.WithLabelValues("обувь")), 0)

	body := scrape(t, r)
	for _, line := range []string{
		// Граница 0.1 с — цель по времени ответа: быстрый запрос в неё
		// попал, медленный нет.
		`pvz_http_request_duration_seconds_bucket{code="201",route="POST /receptions",le="0.1"} 1`,
		`pvz_http_request_duration_seconds_count{code="201",route="POST /receptions"} 2`,
		`pvz_grpc_call_duration_seconds_count{code="OK",method="/pvz.v1.PVZService/GetPVZList"} 1`,
		`pvz_created_total{city="Москва"} 2`,
		`pvz_receptions_opened_total 1`,
		`pvz_receptions_closed_total 1`,
		`pvz_products_added_total{type="обувь"} 1`,
	} {
		assert.Contains(t, body, line+"\n")
	}
	// Стандартные метрики Go и процесса тоже на месте.
	assert.Contains(t, body, "go_goroutines ")
	assert.Contains(t, body, "process_resident_memory_bytes ")
}

// Реестр у каждого Recorder свой: два экземпляра не делят счётчики и не
// конфликтуют при регистрации (с глобальным реестром второй New упал бы).
func TestRecordersAreIndependent(t *testing.T) {
	a, b := New(), New()

	a.ReceptionOpened()

	assert.InDelta(t, 1, testutil.ToFloat64(a.receptionsOpened), 0)
	assert.InDelta(t, 0, testutil.ToFloat64(b.receptionsOpened), 0)
}

// scrape забирает метрики так же, как Prometheus: GET на Handler.
func scrape(t *testing.T, r *Recorder) string {
	t.Helper()
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	return strings.ReplaceAll(string(body), "\r\n", "\n")
}
