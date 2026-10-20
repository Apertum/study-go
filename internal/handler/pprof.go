package handler

import (
	"net/http"
	"strings"

	"study-go.ru/cho/eto/internal/audit"
	"study-go.ru/cho/eto/internal/storage"
)

// PprofHandler — оборачивает net/http/pprof маршруты и проксирует запросы к http.DefaultServeMux.
//
// Копипаст из гугла, наверное не очень. Чтобы сделать безопасно и закрыть. Можно типа на другой порт, но не смотрел как...
// pprof сам регистрирует свои обработчики через init() пакета net/http/pprof на
// http.DefaultServeMux по путям /debug/pprof/*. Этот хендлер просто пробрасывает
// запрос туда, отрезая префикс /debug/pprof/.
//
// Автоматически защищён authMiddleware: для доступа нужна валидная кука user_id.
func PprofHandler(_ *storage.Storage, _ *audit.Central) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Вырезаем /debug/pprof/* -> * (например, /debug/pprof/heap → /heap)
		pprofPath := strings.TrimPrefix(r.URL.Path, "/debug/pprof")
		if pprofPath == "" {
			pprofPath = "/"
		}

		// Проксируем к стандартному pprof-обработчику
		http.DefaultServeMux.ServeHTTP(w, r.WithContext(r.Context()))
	}
}
