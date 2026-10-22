package httptransport

import (
	_ "embed"
	"net/http"

	"github.com/HollowKaeden/pvz-service/api"
)

//go:embed swagger.html
var swaggerPage []byte

// swaggerUI отдаёт страницу Swagger UI. Сама страница статическая, скрипты
// берутся с CDN, спеку она запрашивает рядом: /docs/openapi.yaml.
func swaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(swaggerPage)
}

// openAPISpec отдаёт контракт как есть. Подменять servers не нужно: в нём их
// нет, и Swagger UI шлёт запросы на текущий origin.
func openAPISpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(api.OpenAPI)
}
