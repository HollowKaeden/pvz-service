//go:build integration

package integration

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/transport/grpc/pvzv1"
)

// Бизнес-метрики растут только на то, что действительно записано в базу:
// из 20 одновременных открытий приёмки засчитано одно, неудачное закрытие
// пустой приёмки не засчитано. Технические метрики видят каждый запрос с
// шаблоном маршрута.
func TestMetrics(t *testing.T) {
	resetDB(t)
	moderator, employee := login(t, domain.RoleModerator), login(t, domain.RoleEmployee)
	const (
		pvzCreated        = `pvz_created_total{city="Казань"}`
		opened            = `pvz_receptions_opened_total`
		closed            = `pvz_receptions_closed_total`
		added             = `pvz_products_added_total{type="обувь"}`
		openRejected      = `pvz_http_request_duration_seconds_count{code="400",route="POST /receptions"}`
		closeOK           = `pvz_http_request_duration_seconds_count{code="200",route="POST /pvz/{pvzId}/close_last_reception"}`
		grpcList          = `pvz_grpc_call_duration_seconds_count{code="OK",method="/pvz.v1.PVZService/GetPVZList"}`
		openersInParallel = 20
	)
	before := scrapeMetrics(t)

	pvzID := createPVZ(t, moderator, "Казань")
	statuses := parallel(t, repeat(openersInParallel, request{
		method: http.MethodPost, path: "/receptions", token: employee, body: map[string]any{"pvzId": pvzID},
	}))
	require.Equal(t, map[int]int{http.StatusCreated: 1, http.StatusBadRequest: openersInParallel - 1},
		countStatuses(statuses))
	closePath := "/pvz/" + pvzID.String() + "/close_last_reception"
	mustCall(t, http.StatusBadRequest, http.MethodPost, closePath, employee, nil, nil) // пустую не закрыть
	mustCall(t, http.StatusCreated, http.MethodPost, "/products", employee,
		map[string]any{"pvzId": pvzID, "type": "обувь"}, nil)
	mustCall(t, http.StatusOK, http.MethodPost, closePath, employee, nil, nil)
	_, err := pvzv1.NewPVZServiceClient(grpcConn).GetPVZList(context.Background(), &pvzv1.GetPVZListRequest{})
	require.NoError(t, err)

	after := scrapeMetrics(t)
	delta := func(series string) float64 { return after[series] - before[series] }
	assert.InDelta(t, 1, delta(pvzCreated), 0)
	assert.InDelta(t, 1, delta(opened), 0, "засчитано только открытие, которое прошло")
	assert.InDelta(t, 1, delta(closed), 0, "отказ в закрытии пустой приёмки не засчитан")
	assert.InDelta(t, 1, delta(added), 0)
	assert.InDelta(t, openersInParallel-1, delta(openRejected), 0)
	assert.InDelta(t, 1, delta(closeOK), 0)
	assert.InDelta(t, 1, delta(grpcList), 0)
	for series := range after {
		assert.NotContains(t, series, pvzID.String(), "id из пути не попадает в метрики")
	}
}

// scrapeMetrics забирает метрики, как Prometheus, и возвращает значения
// по рядам: `имя{лейблы}` → значение.
func scrapeMetrics(t *testing.T) map[string]float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	recorder.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	values := map[string]float64{}
	sc := bufio.NewScanner(rec.Body)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Значение — после последнего пробела: в лейблах пробелы бывают
		// ("POST /receptions").
		i := strings.LastIndexByte(line, ' ')
		v, err := strconv.ParseFloat(line[i+1:], 64)
		require.NoError(t, err, line)
		values[line[:i]] = v
	}
	require.NoError(t, sc.Err())
	return values
}
