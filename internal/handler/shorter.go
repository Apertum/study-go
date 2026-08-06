package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/lib/pq"
	"github.com/sirupsen/logrus"
	"study-go.ru/cho/eto/internal/config"
	"study-go.ru/cho/eto/internal/storage"
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

		err = s.PutUnique(uuid, shortID, longURL)
		if err != nil {
			if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" { //  а как ещё
				if existingShortURL, exists := s.GetByOriginalURL(longURL); exists {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					json.NewEncoder(w).Encode(map[string]string{
						"short_url":    existingShortURL,
						"original_url": longURL,
					})
					return
				}
			}
			http.Error(w, "Failed to save", http.StatusConflict)
			return
		}

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

// ShorterBatchPost — обработчик POST /api/shorten/batch
func ShorterBatchPost(s *storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var reqs []struct {
			CorrelationID string `json:"correlation_id"`
			URL           string `json:"url"`
		}
		if err := json.Unmarshal(body, &reqs); err != nil {
			http.Error(w, "Failed to parse request body", http.StatusBadRequest)
			return
		}

		type ResponseItem struct {
			CorrelationID string `json:"correlation_id"`
			UUID          string `json:"uuid"`
			ShortURL      string `json:"short_url"`
			OriginalURL   string `json:"original_url"`
		}

		responses := make([]ResponseItem, 0, len(reqs))
		for _, req := range reqs {
			shortID := config.BaseURL + generateID()
			uuid := s.NextID()

			err := s.PutUnique(uuid, shortID, req.URL)
			if err != nil {
				if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" { //  а как ещё
					if existingShortURL, exists := s.GetByOriginalURL(req.URL); exists {
						responses = append(responses, ResponseItem{
							CorrelationID: req.CorrelationID,
							UUID:          uuid,
							ShortURL:      existingShortURL,
							OriginalURL:   req.URL,
						})
						http.Error(w, "Failed to save1", http.StatusConflict)
					} else {
						responses = append(responses, ResponseItem{
							CorrelationID: req.CorrelationID,
							UUID:          uuid,
							ShortURL:      shortID,
							OriginalURL:   req.URL,
						})
						http.Error(w, "Failed to save2", http.StatusInternalServerError)
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(responses)
					logrus.Error("Случилась ошибка", err)
					return // Выход из функции здесь
				}
			}

			responses = append(responses, ResponseItem{
				CorrelationID: req.CorrelationID,
				UUID:          uuid,
				ShortURL:      shortID,
				OriginalURL:   req.URL,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(responses)
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
func ShorterPing(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if db == nil {
			http.Error(w, "DATABASE is not configured", http.StatusInternalServerError)
			return
		}

		// PingContext с таймаутом для контроля времени ответа
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		if err := db.PingContext(ctx); err != nil {
			logrus.Warn("К БД неконект: ", err)
			http.Error(w, "К БД неконект: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}
}
