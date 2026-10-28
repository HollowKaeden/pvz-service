// Package metrics — метрики pvz для Prometheus: время ответа HTTP и gRPC и
// бизнес-события (заведённые ПВЗ, приёмки, товары).
//
// Остальной код о Prometheus не знает: HTTP-слой, gRPC-слой и сервисы
// объявляют у себя маленькие интерфейсы, а Recorder их реализует.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// durationBuckets — границы гистограмм времени ответа в секундах. Цель по
// заданию — ответ за 100 мс, поэтому 0.1 среди границ: доля быстрых ответов
// считается точно, без интерполяции.
var durationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5}

// Recorder хранит все метрики процесса в своём реестре, а не в глобальном
// реестре client_golang.
type Recorder struct {
	registry *prometheus.Registry

	httpDuration *prometheus.HistogramVec
	grpcDuration *prometheus.HistogramVec

	pvzCreated       *prometheus.CounterVec
	receptionsOpened prometheus.Counter
	receptionsClosed prometheus.Counter
	productsAdded    *prometheus.CounterVec
}

// New создаёт реестр со всеми метриками pvz и стандартными метриками Go и
// процесса (память, горутины, CPU).
func New() *Recorder {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	f := promauto.With(reg)

	return &Recorder{
		registry: reg,
		// Отдельный счётчик запросов не нужен: у гистограммы есть _count.
		httpDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "pvz_http_request_duration_seconds",
			Help:    "Время ответа HTTP API по маршрутам и кодам ответа.",
			Buckets: durationBuckets,
		}, []string{"route", "code"}),
		grpcDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "pvz_grpc_call_duration_seconds",
			Help:    "Время ответа gRPC по методам и кодам ответа.",
			Buckets: durationBuckets,
		}, []string{"method", "code"}),

		pvzCreated: f.NewCounterVec(prometheus.CounterOpts{
			Name: "pvz_created_total",
			Help: "Заведено ПВЗ, по городам.",
		}, []string{"city"}),
		receptionsOpened: f.NewCounter(prometheus.CounterOpts{
			Name: "pvz_receptions_opened_total",
			Help: "Открыто приёмок.",
		}),
		receptionsClosed: f.NewCounter(prometheus.CounterOpts{
			Name: "pvz_receptions_closed_total",
			Help: "Закрыто приёмок.",
		}),
		productsAdded: f.NewCounterVec(prometheus.CounterOpts{
			Name: "pvz_products_added_total",
			Help: "Добавлено товаров в приёмки, по типам.",
		}, []string{"type"}),
	}
}

// Handler отдаёт метрики в формате Prometheus.
func (r *Recorder) Handler() http.Handler {
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{Registry: r.registry})
}

// ObserveHTTP учитывает HTTP-запрос. route — шаблон маршрута, а не путь
// запроса: с UUID в пути у каждого ПВЗ был бы свой ряд метрик.
func (r *Recorder) ObserveHTTP(route string, status int, d time.Duration) {
	r.httpDuration.WithLabelValues(route, strconv.Itoa(status)).Observe(d.Seconds())
}

// ObserveGRPC учитывает gRPC-вызов. code — название gRPC-кода (OK, Internal).
func (r *Recorder) ObserveGRPC(method, code string, d time.Duration) {
	r.grpcDuration.WithLabelValues(method, code).Observe(d.Seconds())
}

// PVZCreated учитывает заведённый ПВЗ.
func (r *Recorder) PVZCreated(city string) { r.pvzCreated.WithLabelValues(city).Inc() }

// ReceptionOpened учитывает открытую приёмку.
func (r *Recorder) ReceptionOpened() { r.receptionsOpened.Inc() }

// ReceptionClosed учитывает закрытую приёмку.
func (r *Recorder) ReceptionClosed() { r.receptionsClosed.Inc() }

// ProductAdded учитывает добавленный товар.
func (r *Recorder) ProductAdded(productType string) {
	r.productsAdded.WithLabelValues(productType).Inc()
}
