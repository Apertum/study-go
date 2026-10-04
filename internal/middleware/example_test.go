package middleware_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"study-go.ru/cho/eto/internal/middleware"
)

// Пример использования GzipMiddleware для ответа без сжатия.
// Клиент не отправляет Accept-Encoding: gzip, поэтому ответ не сжимается.
func ExampleGzipMiddleware_noCompression() {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mw := middleware.GzipMiddleware(inner)

	req := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	fmt.Printf("Status: %d\n", w.Code)
	fmt.Printf("Content-Type: %s\n", w.Header().Get("Content-Type"))
	fmt.Printf("Content-Encoding: %q\n", w.Header().Get("Content-Encoding"))

	// Выход:
	// Status: 200
	// Content-Type: application/json
	// Content-Encoding: ""
}

// Пример использования GzipMiddleware для gzip-сжатого ответа.
// Клиент отправляет Accept-Encoding: gzip, и middleware сжимает ответ.
func ExampleGzipMiddleware_gzipResponse() {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"message": "Hello, World!"})
	})

	mw := middleware.GzipMiddleware(inner)

	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	fmt.Printf("Status: %d\n", w.Code)
	fmt.Printf("Content-Encoding: %s\n", w.Header().Get("Content-Encoding"))
	fmt.Printf("Body compressed: %v\n", w.Body.Len() < 50)

	// Выход:
	// Status: 200
	// Content-Encoding: gzip
	// Body compressed: true
}

// Пример использования GzipMiddleware для gzip-запроса.
// Клиент отправляет тело запроса в gzip-формате.
func ExampleGzipMiddleware_gzipRequest() {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("Received Content-Encoding: %s\n", r.Header.Get("Content-Encoding"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mw := middleware.GzipMiddleware(inner)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	json.NewEncoder(gz).Encode(map[string]string{"key": "value"})
	gz.Close()

	req := httptest.NewRequest("POST", "/api/webhook", &buf)
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	// Выход:
	// Received Content-Encoding: gzip
}

// Пример распаковки gzip-ответа после прохождения через middleware.
// Показывает, что сжатый ответ можно прочитать через gzip.Reader.
func ExampleGzipMiddleware_decodeResponse() {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"data": "test-value"})
	})

	mw := middleware.GzipMiddleware(inner)

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	reader, _ := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	defer reader.Close()

	body, _ := io.ReadAll(reader)
	var result map[string]interface{}
	json.Unmarshal(body, &result)

	fmt.Printf("Decoded data: %s\n", result["data"])

	// Выход: Decoded data: test-value
}
