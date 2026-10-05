package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"study-go.ru/cho/eto/internal/handler"
	"study-go.ru/cho/eto/internal/middleware"
	"study-go.ru/cho/eto/internal/storage"
)

// просто всё подряд запустим, типа потестили и замерили. graphviz не смотрел, потом посмотреть что там визуализируется

// newCleanStore создаёт новое хранилище с уникальным временным путём для изоляции бенчмарков.
func newCleanStore(t *testing.B) *storage.Storage {
	t.Cleanup(func() {}) // не нужно удалять файлы — storage in-memory по умолчанию
	return storage.New("")
}

// BenchmarkShorterGet_not_found — чтение несуществующей короткой ссылки (404).
func BenchmarkShorterGet_not_found(b *testing.B) {
	store := newCleanStore(b)
	h := handler.ShorterGet(store, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/nonexistent-id", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
	}
}

// BenchmarkShorterPost_single — сокращение одной ссылки.
func BenchmarkShorterPost_single(b *testing.B) {
	store := newCleanStore(b)
	h := handler.ShorterPost(store, nil)

	bodyTemplate := `{"url":"https://example.com/resource/%d"}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body := strings.NewReader(bodyTemplate)
		req := httptest.NewRequest("POST", "/api/shorten", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
	}
}

// BenchmarkShorterBatchPost_10 — пакетное сокращение 10 ссылок.
func BenchmarkShorterBatchPost_10(b *testing.B) {
	store := newCleanStore(b)
	h := handler.ShorterBatchPost(store, nil)

	type batchReq struct {
		CorrelationID string `json:"correlation_id"`
		URL           string `json:"url"`
	}
	reqs := make([]batchReq, 10)
	for i := 0; i < 10; i++ {
		reqs[i] = batchReq{
			CorrelationID: "req-0000000" + string(rune('a'+i)),
			URL:           "https://example.com/resource/" + string(rune('a'+i)),
		}
	}
	body, _ := json.Marshal(reqs)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/api/shorten/batch", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
	}
}

// BenchmarkShorterBatchPost_100 — пакетное сокращение 100 ссылок.
func BenchmarkShorterBatchPost_100(b *testing.B) {
	store := newCleanStore(b)
	h := handler.ShorterBatchPost(store, nil)

	type batchReq struct {
		CorrelationID string `json:"correlation_id"`
		URL           string `json:"url"`
	}
	reqs := make([]batchReq, 100)
	for i := 0; i < 100; i++ {
		suffix := ""
		if i < 10 {
			suffix = "0" + string(rune('0'+i))
		} else if i < 99 {
			suffix = string(rune('a'+(i-10)%26)) + string(rune('0'+(i/26)%10))
		} else {
			suffix = "zz"
		}
		reqs[i] = batchReq{
			CorrelationID: "req-" + suffix,
			URL:           "https://example.com/resource/" + suffix,
		}
	}
	body, _ := json.Marshal(reqs)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/api/shorten/batch", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
	}
}

// BenchmarkGzipMiddleware_enabled — middleware с включённым gzip.
func BenchmarkGzipMiddleware_enabled(b *testing.B) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"message": "Hello, World!"})
	})

	mw := middleware.GzipMiddleware(inner)

	body := []byte(`{"message":"Hello, World!"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/", bytes.NewReader(body))
		req.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
	}
}

// BenchmarkGzipMiddleware_disabled — middleware без gzip.
func BenchmarkGzipMiddleware_disabled(b *testing.B) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"message": "Hello, World!"})
	})

	mw := middleware.GzipMiddleware(inner)

	body := []byte(`{"message":"Hello, World!"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/", bytes.NewReader(body))
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
	}
}
