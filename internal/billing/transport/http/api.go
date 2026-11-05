// Package httptransport реализует публичный API чтения начислений.
package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/HollowKaeden/pvz-service/internal/billing/domain"
	"github.com/HollowKaeden/pvz-service/internal/billing/transport/http/oapi"
)

// Reader нужен только HTTP-слою, потребитель событий от него не зависит.
type Reader interface {
	Balances(context.Context, int, int) ([]domain.Balance, error)
	Journal(context.Context, uuid.UUID, int, int) ([]domain.Payout, error)
}

// Pinger проверяет только БД. Kafka на доступность API не влияет.
type Pinger interface{ Ping(context.Context) error }

// RequestMetrics принимает шаблоны маршрутов без пользовательских UUID.
type RequestMetrics interface{ ObserveHTTP(string, int, float64) }

// API преобразует доменную модель в DTO контракта billing.
type API struct {
	reader Reader
	db     Pinger
	rates  []domain.Rate
}

// NewRouter регистрирует strict handlers без авторизации (публичное демо).
func NewRouter(log *slog.Logger, reader Reader, db Pinger, rates []domain.Rate, metrics RequestMetrics) http.Handler {
	a := API{reader: reader, db: db, rates: rates}
	bad := func(w http.ResponseWriter, _ *http.Request, _ error) {
		writeError(w, http.StatusBadRequest, "Некорректные параметры запроса")
	}
	failure := func(w http.ResponseWriter, r *http.Request, err error) {
		log.ErrorContext(r.Context(), "ошибка API billing", slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "Внутренняя ошибка сервера")
	}
	strict := oapi.NewStrictHandlerWithOptions(a, nil, oapi.StrictHTTPServerOptions{RequestErrorHandlerFunc: bad, ResponseErrorHandlerFunc: failure})
	handler := oapi.HandlerWithOptions(strict, oapi.StdHTTPServerOptions{ErrorHandlerFunc: bad})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		w.Header().Set("Cache-Control", "no-store")
		r = r.WithContext(ctx)
		started := time.Now()
		response := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		handler.ServeHTTP(response, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		if metrics != nil {
			metrics.ObserveHTTP(route, response.status, time.Since(started).Seconds())
		}
		log.InfoContext(ctx, "HTTP billing", slog.String("route", route), slog.Int("status", response.status), slog.Duration("duration", time.Since(started)))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(oapi.Error{Message: message})
}

func pagination(page, limit *int) (int, int, error) {
	p, n := 1, 10
	if page != nil {
		p = *page
	}
	if limit != nil {
		n = *limit
	}
	if p < 1 || p > 10000 || n < 1 || n > 30 {
		return 0, 0, errors.New("page: 1–10000, limit: 1–30")
	}
	return n, (p - 1) * n, nil
}

// GetHealth проверяет БД с ограничением времени.
func (a API) GetHealth(ctx context.Context, _ oapi.GetHealthRequestObject) (oapi.GetHealthResponseObject, error) {
	if err := a.db.Ping(ctx); err != nil {
		return oapi.GetHealth503JSONResponse{ErrorJSONResponse: oapi.ErrorJSONResponse{Message: "База данных недоступна"}}, nil //nolint:nilerr // Ошибка превращена в контрактный ответ 503.
	}
	return oapi.GetHealth200JSONResponse{Status: oapi.Ok}, nil
}

// ListBalances отдаёт страницу балансов, в том числе пустой массив.
func (a API) ListBalances(ctx context.Context, r oapi.ListBalancesRequestObject) (oapi.ListBalancesResponseObject, error) {
	limit, offset, err := pagination(r.Params.Page, r.Params.Limit)
	if err != nil {
		return oapi.ListBalances400JSONResponse{ErrorJSONResponse: oapi.ErrorJSONResponse{Message: err.Error()}}, nil //nolint:nilerr // Ошибка параметров превращена в контрактный ответ 400.
	}
	items, err := a.reader.Balances(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	result := make(oapi.ListBalances200JSONResponse, 0, len(items))
	for _, v := range items {
		result = append(result, oapi.Balance{PVZID: v.PVZID, City: v.City, AmountKopecks: v.Amount, UpdatedAt: v.UpdatedAt})
	}
	return result, nil
}

// GetJournal показывает новые начисления первыми.
func (a API) GetJournal(ctx context.Context, r oapi.GetJournalRequestObject) (oapi.GetJournalResponseObject, error) {
	limit, offset, err := pagination(r.Params.Page, r.Params.Limit)
	if err != nil {
		return oapi.GetJournal400JSONResponse{ErrorJSONResponse: oapi.ErrorJSONResponse{Message: err.Error()}}, nil //nolint:nilerr // Ошибка параметров превращена в контрактный ответ 400.
	}
	items, err := a.reader.Journal(ctx, r.PVZID, limit, offset)
	if err != nil {
		return nil, err
	}
	result := make(oapi.GetJournal200JSONResponse, 0, len(items))
	for _, v := range items {
		result = append(result, oapi.Payout{EventID: v.EventID, ReceptionID: v.ReceptionID, PVZID: v.PVZID, City: v.City, AmountKopecks: v.Amount, Products: v.Products, ClosedAt: v.ClosedAt, ProcessedAt: v.ProcessedAt})
	}
	return result, nil
}

// GetRates показывает кэш ставок процесса.
func (a API) GetRates(_ context.Context, _ oapi.GetRatesRequestObject) (oapi.GetRatesResponseObject, error) {
	result := make(oapi.GetRates200JSONResponse, 0, len(a.rates))
	for _, r := range a.rates {
		result = append(result, oapi.Rate{ProductType: r.ProductType, AmountKopecks: r.Amount})
	}
	return result, nil
}
