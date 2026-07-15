package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"study-go.ru/cho/eto/internal/config"
)

// хранилище ссылок в памяти (ключ — короткий ID, значение — оригинальный URL)
var (
	mu    sync.Mutex
	store = make(map[string]string)
)

// generateID создаёт случайный короткий ID
func generateID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return config.BaseUrl + hex.EncodeToString(b)
}

// ShorterPost — обработчик POST /
// Принимает URL в теле запроса (JSON или raw), сохраняет, возвращает короткий ID
func ShorterPost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// пытаемся распарсить как JSON, иначе берём raw-строку
	var longURL string
	if r.Header.Get("Content-Type") == "application/json" {
		var req struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(body, &req); err == nil {
			longURL = req.URL
		}
	}
	if longURL == "" {
		longURL = string(body)
	}

	// генерируем ID и сохраняем
	result := generateID()

	mu.Lock()
	store[result] = longURL
	mu.Unlock()

	// возвращаем короткий ID
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"result":  result,
		"url": longURL,
	})
}

// ShorterGet — обработчик GET /{id}
// Возвращает оригинальный URL по короткому ID
func ShorterGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	mu.Lock()
	longURL, ok := store[id]
	mu.Unlock()

	if !ok {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	// перенаправляем на оригинальный URL
	http.Redirect(w, r, longURL, http.StatusTemporaryRedirect)
}
