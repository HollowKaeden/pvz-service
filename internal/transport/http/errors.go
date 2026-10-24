package httptransport

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/transport/http/oapi"
)

// errNotImplemented возвращают ручки, логика которых ещё не написана.
var errNotImplemented = errors.New("not implemented")

// badRequest отвечает 400, когда запрос не разобрался: невалидный JSON,
// UUID в пути, параметр query. Вызывается сгенерированным кодом до ручки.
func badRequest(w http.ResponseWriter, _ *http.Request, err error) {
	writeError(w, http.StatusBadRequest, "неверный запрос: "+err.Error())
}

// handlerError переводит ошибку ручки в HTTP-ответ. Здесь единственное место,
// где ошибки превращаются в коды ответа.
func handlerError(log *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if status, ok := domainStatus(err); ok {
			writeError(w, status, err.Error())
			return
		}
		if errors.Is(err, errNotImplemented) {
			writeError(w, http.StatusNotImplemented, "ручка ещё не реализована")
			return
		}

		// Неожиданная ошибка: подробности только в лог, клиенту общий текст.
		log.ErrorContext(r.Context(), "ошибка обработки запроса",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Any("error", err),
		)
		writeError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
	}
}

// domainStatus возвращает HTTP-код для ошибки бизнес-правила. Только такие
// ошибки показываются клиенту с исходным текстом.
func domainStatus(err error) (int, bool) {
	switch {
	case errors.Is(err, domain.ErrInvalidRole),
		errors.Is(err, domain.ErrCityNotAllowed),
		errors.Is(err, domain.ErrReceptionAlreadyOpen),
		errors.Is(err, domain.ErrNoOpenReception),
		errors.Is(err, domain.ErrProductTypeNotAllowed),
		errors.Is(err, domain.ErrNoProductsToDelete):
		return http.StatusBadRequest, true
	// В контракте 404 нет, там такой запрос назван неверным (400). Но ПВЗ из
	// запроса не существует, и по HTTP это 404: так клиент отличит опечатку в
	// id от нарушенного правила.
	case errors.Is(err, domain.ErrPVZNotFound):
		return http.StatusNotFound, true
	default:
		return 0, false
	}
}

// writeError пишет ошибку в формате контракта: {"message": "..."}.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // это API, а не HTML: угловые скобки в тексте оставляем как есть
	_ = enc.Encode(oapi.Error{Message: msg})
}
