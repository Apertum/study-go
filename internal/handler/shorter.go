package handler

import (
	"database/sql"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"study-go.ru/cho/eto/internal/config"
	"study-go.ru/cho/eto/internal/storage"

	_ "github.com/lib/pq"
)

// generateID создаёт случайный короткий ID
func generateID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ShorterPost — обработчик POST /
// Принимает URL в теле запроса (JSON или raw), сохраняет, возвращает короткий ID
func ShorterPost(s *storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		shortID := config.BaseURL + generateID()
		uuid := s.NextID()

		s.Put(uuid, shortID, longURL)

		// возвращаем короткий ID
		w.Header().Set("Content-Type", "application/json")
		// Порядок важен: WriteHeader отправляет заголовки клиенту.
		// Если вызвать w.Write() раньше, Go автоматически отправит код 200 OK, и изменить его на 201 уже не получится.
		// можно вызвать только один раз за один
		w.WriteHeader(http.StatusCreated) // Возвращает статус 201
		json.NewEncoder(w).Encode(map[string]string{
			"uuid":         uuid,
			"short_url":    shortID,
			"original_url": longURL,
		})
	}
}

// ShorterGet — обработчик GET /{id}
// Возвращает оригинальный URL по короткому ID
func ShorterGet(s *storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		longURL, ok := s.Get(id)
		if !ok {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		// перенаправляем на оригинальный URL
		http.Redirect(w, r, longURL, http.StatusTemporaryRedirect)
	}
}

// ShorterPing — обработчик GET /ping
// Проверяет соединение с PostgreSQL базой данных
func ShorterPing() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if config.DatabaseDSN == "" {
			http.Error(w, "DATABASE_DSN not configured", http.StatusInternalServerError)
			return
		}

		db, err := sql.Open("postgres", config.DatabaseDSN)
		if err != nil {
			http.Error(w, "Failed to open database", http.StatusInternalServerError)
			return
		}
		defer db.Close()

		if err := db.Ping(); err != nil {
			http.Error(w, "Database connection failed", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}
}
