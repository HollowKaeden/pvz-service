package httptransport

import (
	"net/http"
	"strings"

	"github.com/HollowKaeden/pvz-service/web"
)

// contentSecurityPolicy — страница фронтенда грузит скрипты, стили и шрифты
// только со своего origin и ходит только в свой API. Встроенных скриптов и
// стилей нет, поэтому 'unsafe-inline' не нужен.
const contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// frontendPage отдаёт страницу фронтенда на /.
func frontendPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	// Имена файлов без хеша, поэтому браузер перепроверяет их при каждом
	// открытии: после деплоя никто не застрянет на старом JS.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, web.FS, "index.html")
}

// frontendAssets отдаёт CSS, JS, шрифты и иконку на /assets/. Путь запроса
// совпадает с путём внутри web.FS. Список файлов каталога не показывается.
func frontendAssets() http.Handler {
	files := http.FileServerFS(web.FS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}
