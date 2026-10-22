package httptransport

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

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

// writeError пишет ошибку в формате контракта: {"message": "..."}.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(oapi.Error{Message: msg})
}
