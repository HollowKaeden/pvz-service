// Package metrics содержит отдельный реестр Prometheus сервиса начислений.
package metrics

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Recorder не использует глобальный реестр и не зависит от метрик pvz.
type Recorder struct {
	Registry                            *prometheus.Registry
	processed, duplicates, amount, dead prometheus.Counter
	Lag                                 *prometheus.GaugeVec
	DLQ                                 prometheus.Gauge
	HTTP                                *prometheus.HistogramVec
}

// New создаёт метрики обработки, дубликатов, копеек, DLQ и отставания партиций.
func New() *Recorder {
	r := &Recorder{Registry: prometheus.NewRegistry()}
	counter := func(name, help string) prometheus.Counter {
		c := prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help})
		r.Registry.MustRegister(c)
		return c
	}
	r.processed = counter("billing_events_processed_total", "Успешные начисления.")
	r.duplicates = counter("billing_duplicates_total", "Дубликаты без повторного начисления.")
	r.amount = counter("billing_accrued_kopecks_total", "Начисленные целые копейки.")
	r.dead = counter("billing_dlq_total", "Сообщения, подтверждённые DLQ за время процесса.")
	r.Lag = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "billing_consumer_lag", Help: "Необработанные сообщения партиции."}, []string{"partition"})
	r.DLQ = prometheus.NewGauge(prometheus.GaugeOpts{Name: "billing_dlq_size", Help: "Сообщения, оставшиеся в DLQ (end минус start)."})
	r.Registry.MustRegister(r.Lag, r.DLQ)
	r.HTTP = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "billing_http_request_duration_seconds", Help: "Время HTTP-запросов billing."}, []string{"route", "code"})
	r.Registry.MustRegister(r.HTTP)
	return r
}

// ObserveHTTP учитывает шаблон маршрута, код ответа и время обработки.
func (r *Recorder) ObserveHTTP(route string, code int, seconds float64) {
	r.HTTP.WithLabelValues(route, strconv.Itoa(code)).Observe(seconds)
}

// Applied вызывается только после коммита БД.
func (r *Recorder) Applied(amount int64, duplicate bool) {
	if duplicate {
		r.duplicates.Inc()
		return
	}
	r.processed.Inc()
	r.amount.Add(float64(amount))
}

// Dead учитывает подтверждённую отправку в DLQ.
func (r *Recorder) Dead() { r.dead.Inc() }

// SetLag обновляет лаг одной партиции без UUID в метках.
func (r *Recorder) SetLag(partition int32, lag int64) {
	r.Lag.WithLabelValues(strconv.FormatInt(int64(partition), 10)).Set(float64(max(0, lag)))
}

// Handler отдаёт только реестр billing.
func (r *Recorder) Handler() http.Handler {
	return promhttp.HandlerFor(r.Registry, promhttp.HandlerOpts{})
}
