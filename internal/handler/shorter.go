package handler

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/sirupsen/logrus"
	"study-go.ru/cho/eto/internal/config"
	"study-go.ru/cho/eto/internal/storage"
)

// контекстный ключ для user ID
type contextKey struct{}

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

		// извлекаем usrID из контекста (установлен AuthMiddleware)
		usrID, ok := r.Context().Value(contextKey{}).(int)
		if !ok {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// генерируем ID и сохраняем
		shortID := config.BaseURL + generateID()
		uuid := s.NextID()

		err = s.PutUnique(uuid, shortID, longURL, usrID)
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

		// извлекаем usrID из контекста (установлен AuthMiddleware)
		usrID, ok := r.Context().Value(contextKey{}).(int)
		if !ok {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
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

			err := s.PutUnique(uuid, shortID, req.URL, usrID)
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

		longURL, deleted, err := s.Get(id)
		if err != nil || longURL == "" {
			logrus.Error("Not found by ERROR. ", http.StatusNotFound)
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		if longURL != "" && deleted {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGone)
			json.NewEncoder(w).Encode("{\"url\": \"Gone\"}")
		} else {
			// перенаправляем на оригинальный URL
			http.Redirect(w, r, longURL, http.StatusTemporaryRedirect)
		}
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

// signinCookie создаёт HMAC-SHA256 подпись для payload и возвращает строку signature.
// SigninCookie — экспортированная версия для тестов.
func signinCookie(cookieKey, payload string) string {
	h := hmac.New(sha256.New, []byte(cookieKey))
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

// SigninCookieForTest — экспортированная версия для использования в тестах.
func SigninCookieForTest(cookieKey, payload string) string {
	return signinCookie(cookieKey, payload)
}

// verifyCookie проверяет HMAC-SHA256 подпись. Возвращает payload, если подпись валидна, иначе ошибку.
func verifyCookie(cookieKey, payload, signature string) (string, error) {
	expectedSig := signinCookie(cookieKey, payload)
	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
		return "", http.ErrAbortHandler
	}
	return payload, nil
}

// AuthMiddleware — middleware для проверки авторизации через куку user_id.
// Если куки нет — 401. Если подпись невалидна — 401.
// Если пользователь не найден в БД — 403.
// Если всё ок — кладёт user ID в контекст и передаёт запрос дальше.
func AuthMiddleware(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("user_id")
			if err != nil || cookie.Value == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// parse "payload:signature"
			parts := strings.SplitN(cookie.Value, ":", 3)
			if len(parts) != 2 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			payload, signature := parts[0], parts[1]

			// верифицируем HMAC
			if _, err := verifyCookie(config.CookieKey, payload, signature); err != nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// извлекаем user ID
			usrID, err := strconv.Atoi(payload)
			if err != nil {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			// проверяем что пользователь существует в БД
			var exists int
			err = db.QueryRowContext(r.Context(), "SELECT 1 FROM usr WHERE id = $1", usrID).Scan(&exists)
			if err == sql.ErrNoRows {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			} else if err != nil {
				logrus.WithError(err).Error("AuthMiddleware DB error")
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}

			// кладём user ID в контекст
			ctx := context.WithValue(r.Context(), contextKey{}, usrID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}


// ShorterLoginPost — обработчик POST /login
// Принимает JSON {"usr_name": "someName"}, ищет пользователя по usr_name в БД,
// если не находит — создаёт нового (paswd пустой).
// Выдаёт симметрично подписанную куку с user ID.
func ShorterLoginPost(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var req struct {
			UsrName string `json:"usr_name"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.UsrName == "" {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Ищем пользователя по usr_name
		var usrID int
		err = db.QueryRowContext(r.Context(), "SELECT id FROM usr WHERE usr_name = $1", req.UsrName).Scan(&usrID)
		if err == sql.ErrNoRows {
			// Не найден — создаём нового пользователя
			// paswd пустой, т.к. для задачи не требуется аутентификация по паролю
			err = db.QueryRowContext(r.Context(),
				"INSERT INTO usr (usr_name, paswd) VALUES ($1, '') RETURNING id",
				req.UsrName).Scan(&usrID)
			if err != nil {
				logrus.WithError(err).Error("Failed to create user")
				http.Error(w, "Failed to create user", http.StatusInternalServerError)
				return
			}
		} else if err != nil {
			logrus.WithError(err).Error("Failed to query user")
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		// Формируем payload и подпись
		payload := fmt.Sprintf("%d", usrID)
		signature := signinCookie(config.CookieKey, payload)

		// Устанавливаем куку: значение = "payload:signature"
		cookie := &http.Cookie{
			Name:     "user_id",
			Value:    payload + ":" + signature,
			Path:     "/",
			HttpOnly: true,
			Secure:   false, // для локальной разработки можно false; в прод — true + SameSite=None
			SameSite: http.SameSiteLaxMode,
			MaxAge:   30 * 24 * 3600, // 30 дней
		}
		http.SetCookie(w, cookie)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"user_id": payload,
		})
	}
}

// ShorterUserURLsGet — обработчик GET /api/user/urls
// Возвращает все сокращённые пользователем URL из БД.
// Если URL нет — 204 No Content.
func ShorterUserURLsGet(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// извлекаем usrID из контекста (установлен AuthMiddleware)
		usrID, ok := r.Context().Value(contextKey{}).(int)
		if !ok {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		type urlEntry struct {
			ShortURL    string `json:"short_url"`
			OriginalURL string `json:"original_url"`
		}

		rows, err := db.QueryContext(r.Context(),
			"SELECT short_url, original_url FROM url_srv WHERE usr_id = $1 ORDER BY id",
			usrID,
		)
		if err != nil {
			logrus.WithError(err).Error("Failed to query user URLs")
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var urls []urlEntry
		for rows.Next() {
			var entry urlEntry
			if err := rows.Scan(&entry.ShortURL, &entry.OriginalURL); err != nil {
				logrus.WithError(err).Error("Failed to scan URL row")
				http.Error(w, "Database error", http.StatusInternalServerError)
				return
			}
			urls = append(urls, entry)
		}
		if err := rows.Err(); err != nil {
			logrus.WithError(err).Error("Error iterating URL rows")
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		if urls == nil || len(urls) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(urls)
	}
}

func DeleteURLs(s *storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// Создаем переменную для результата
		var forDel []string

		// Декодируем, преобразуя строку в срез байт []byte
		err = json.Unmarshal(body, &forDel)
		if err != nil {
			fmt.Printf("Ошибка парсинга: %v\n", err)
			return
		}
		logrus.Infof("\nПолученный срез для удаления: %v", forDel)
		logrus.Infof("Длина среза: %d\n", len(forDel))

		// извлекаем usrID из контекста (установлен AuthMiddleware)
		usrID, ok := r.Context().Value(contextKey{}).(int)
		if !ok {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		go s.DeleteUrls(r.Context(), usrID, forDel)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode("{\"done\":  \"OK\"}")

	}
}
