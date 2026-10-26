package httptransport

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/transport/http/oapi"
)

// badRequest отвечает 400, когда запрос не разобрался: невалидный JSON,
// UUID в пути, параметр query. Вызывается сгенерированным кодом до ручки.
// Слишком большое тело — 413.
func badRequest(w http.ResponseWriter, _ *http.Request, err error) {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		writeError(w, http.StatusRequestEntityTooLarge, "тело запроса больше 1 МБ")
		return
	}
	writeError(w, http.StatusBadRequest, "неверный запрос: "+err.Error())
}

// handlerError переводит ошибку ручки в HTTP-ответ. Здесь единственное место,
// где ошибки превращаются в коды ответа.
func handlerError(log *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if status, ok := domainStatus(err); ok {
			if status == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", "Bearer")
			}
			writeError(w, status, err.Error())
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
		errors.Is(err, domain.ErrReceptionEmpty),
		errors.Is(err, domain.ErrProductTypeNotAllowed),
		errors.Is(err, domain.ErrNoProductsToDelete),
		errors.Is(err, domain.ErrInvalidListParams),
		errors.Is(err, domain.ErrInvalidPassword),
		// Занятый email — нарушенное правило, как «приёмка уже открыта»,
		// поэтому 400 из контракта, а не 409.
		errors.Is(err, domain.ErrEmailTaken):
		return http.StatusBadRequest, true
	case errors.Is(err, domain.ErrInvalidCredentials):
		return http.StatusUnauthorized, true
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
