package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"study-go.ru/cho/eto/internal/handler"
	"study-go.ru/cho/eto/internal/middleware"
	"study-go.ru/cho/eto/internal/storage"
)

// ExampleShorterGet_not_found демонстрирует обработку запроса к несуществующей короткой ссылке.
// GET /{id} возвращает 404 Not Found, если короткая ссылка не найдена в хранилище.
func ExampleShorterGet_not_found() {
	store := storage.New("")
	h := handler.ShorterGet(store, nil)

	req := httptest.NewRequest("GET", "/nonexistent-id", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	fmt.Printf("Status: %d\nBody: %s", rr.Code, rr.Body.String())
	// Output: Status: 404
	// Body: Not found
}

// ExampleSigninCookieForTest демонстрирует генерацию HMAC-SHA256 подписи для куки.
// Возвращает строку из 64 hex-символов (256 бит = 32 байта).
func ExampleSigninCookieForTest() {
	key := "my-secret-key"
	payload := "42"

	signature := handler.SigninCookieForTest(key, payload)

	fmt.Printf("Signature length: %d chars\nCookie format: user_id=%s:<signature>", len(signature), payload)
	// Output: Signature length: 64 chars
	// Cookie format: user_id=42:<signature>
}

// ExampleGzipMiddleware_compression демонстрирует сжатие ответа через GzipMiddleware.
// Когда клиент отправляет Accept-Encoding: gzip, middleware сжимает ответ для
// application/json и text/html контента.
func ExampleGzipMiddleware_compression() {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mw := middleware.GzipMiddleware(inner)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	fmt.Printf("Status: %d\nContent-Encoding: %q", w.Code, w.Header().Get("Content-Encoding"))
	// Output: Status: 200
	// Content-Encoding: "gzip"
}

// ExampleGzipMiddleware_no_compression демонстрирует, что не-gzip клиенты получают
// uncompressed ответ. Если клиент не отправил Accept-Encoding: gzip, middleware
// пропускает ответ без сжатия.
func ExampleGzipMiddleware_no_compression() {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mw := middleware.GzipMiddleware(inner)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	fmt.Printf("Content-Type: %s\nBody length: %d", w.Header().Get("Content-Type"), len(w.Body.String()))
	// Output: Content-Type: application/json
	// Body length: 16
}
