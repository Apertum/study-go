// Package handler предоставляет HTTP-хендлеры и middleware для URL-сократителя.
//
// Экспортируемые хендлеры:
//   - ShorterPost — POST /  и POST /api/shorten
//   - ShorterBatchPost — POST /api/shorten/batch
//   - ShorterGet — GET /{id}
//   - ShorterPing — POST /ping
//   - ShorterLoginPost — POST /login
//   - ShorterUserURLsGet — GET /api/user/urls
//
// Экспортируемое middleware:
//   - AuthMiddleware — проверка HMAC-подписанной куки user_id
//
// Экспортируемые утилиты:
//   - SigninCookieForTest — генерация HMAC-подписи для тестов
package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/lib/pq"
	"github.com/sirupsen/logrus"
	"study-go.ru/cho/eto/internal/audit"
	"study-go.ru/cho/eto/internal/authn"
	"study-go.ru/cho/eto/internal/config"
	"study-go.ru/cho/eto/internal/shorten"
	"study-go.ru/cho/eto/internal/storage"
)

// контекстный ключ для user ID
type contextKey struct{}

// Хранилище и центр аудита удовлетворяют зависимостям доменного слоя: проверка на этапе
// компиляции дешевле, чем проверка в рантайме.
var (
	_ shorten.ShortStore = (*storage.Storage)(nil)
	_ shorten.Auditor    = (*audit.Central)(nil)
)

// generateID создаёт случайный короткий ID.
//
// Имя сохранено: тесты пакета обращаются к функции напрямую.
func generateID() string { return shorten.GenerateID() }

// newService собирает доменный слой. Проверка central != nil обязательна: nil-указатель,
// положенный в интерфейс, был бы значением, не равным nil, и аудит пытались бы ставить
// через него.
func newService(s *storage.Storage, central *audit.Central) *shorten.Service {
	var auditor shorten.Auditor
	if central != nil {
		auditor = central
	}

	return &shorten.Service{Store: s, Audit: auditor, BaseURL: config.BaseURL}
}

// ShorterPost обрабатывает POST-запросы на пути / и /api/shorten.
//
// Принимает URL в теле запроса (JSON с полем url или raw-строка), сохраняет
// сокращённую версию и возвращает 201 Created с коротким ID.
// Если original_url уже существует, возвращает 409 Conflict с существующим short_url.
//
// Требует авторизацию через HMAC-подписанную куку user_id (проверяется AuthMiddleware).
//
// Пример JSON-запроса:
//
//	curl -X POST http://localhost:8080/api/shorten \
//	  -b "user_id=1:signature" \
//	  -H "Content-Type: application/json" \
//	  -d '{"url":"https://example.com/very/long/path"}'
//
// Ответ 201:
//
//	{"uuid":"0","short_url":"http://short.ru/a1b2c3d4","original_url":"https://example.com/very/long/path"}
//
// Ответ 409 (дубликат):
//
//	{"short_url":"http://short.ru/a1b2c3d4","original_url":"https://example.com/very/long/path"}
func ShorterPost(s *storage.Storage, central *audit.Central) http.HandlerFunc {
	svc := newService(s, central)

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
			if err = json.Unmarshal(body, &req); err == nil {
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

		result, err := svc.Shorten(usrID, longURL)
		if err != nil {
			if dup, ok := errors.AsType[*shorten.DuplicateError](err); ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{
					"short_url":    dup.ShortURL,
					"original_url": longURL,
				})
				return
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
			"uuid":         result.UUID,
			"short_url":    result.ShortURL,
			"original_url": result.OriginalURL,
		})
	}
}

// ShorterBatchPost обрабатывает POST-запросы на пути /api/shorten/batch.
//
// Принимает массив объектов {correlation_id, url}, для каждого создаёт
// сокращённую ссылку и возвращает массив результатов с 201 Created.
//
// Требует авторизацию через HMAC-подписанную куку user_id (проверяется AuthMiddleware).
//
// Пример запроса:
//
//	curl -X POST http://localhost:8080/api/shorten/batch \
//	  -b "user_id=1:signature" \
//	  -H "Content-Type: application/json" \
//	  -d '[{"correlation_id":"req-1","url":"https://example.com/one"},{"correlation_id":"req-2","url":"https://example.com/two"}]'
//
// Ответ 201:
//
//	[{"correlation_id":"req-1","uuid":"0","short_url":"http://short.ru/a1b2c3d4","original_url":"https://example.com/one"}]
func ShorterBatchPost(s *storage.Storage, central *audit.Central) http.HandlerFunc {
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
		userIDStr := strconv.FormatInt(int64(usrID), 10)
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

			// Аудит после успешного сохранения каждого URL в батче
			if central != nil {
				central.Notify(audit.Event{
					Timestamp: time.Now().Unix(),
					Action:    audit.ActionShorten,
					UserID:    userIDStr,
					URL:       req.URL,
				})
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(responses)
	}
}

// ShorterGet обрабатывает GET-запросы на пути /{id}.
//
// Возвращает 307 Temporary Redirect на оригинальный URL по короткому ID.
// Если запись удалена, возвращает 410 Gone. Если не найдена — 404 Not Found.
// Не требует авторизации.
//
// Пример запроса:
//
//	curl -v http://localhost:8080/a1b2c3d4
//
// Ответ 307:
//
//	HTTP/1.1 307 Temporary Redirect
//	Location: https://example.com/very/long/path
func ShorterGet(s *storage.Storage, central *audit.Central) http.HandlerFunc {
	svc := newService(s, central)

	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		longURL, err := svc.Expand(id)
		if errors.Is(err, shorten.ErrGone) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGone)
			json.NewEncoder(w).Encode("{\"url\": \"Gone\"}")
			return
		}
		if err != nil {
			logrus.Error("Not found by ERROR. ", http.StatusNotFound)
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		// перенаправляем на оригинальный URL
		http.Redirect(w, r, longURL, http.StatusTemporaryRedirect)
	}
}

// ShorterPing обрабатывает POST-запросы на пути /ping.
//
// Проверяет доступность PostgreSQL-базы данных через PingContext.
// Не требует авторизации.
//
// Пример запроса:
//
//	curl http://localhost:8080/ping
//
// Ответ 200:
//
//	OK
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

// SigninCookieForTest генерирует HMAC-SHA256 подпись для заданных cookieKey и payload.
//
// Экспортирована исключительно для использования в тестах. Подпись создаёт общий
// authn.Sign — его же проверяют HTTP-middleware и gRPC-адаптер.
func SigninCookieForTest(cookieKey, payload string) string {
	return authn.Sign(cookieKey, payload)
}

// AuthMiddleware — HTTP-middleware для проверки авторизации через куку user_id.
//
// Работает по следующему алгоритму:
//  1. Читает куку user_id; если отсутствует — возвращает 401 Unauthorized.
//  2. Проверяет значение вида "payload:signature" общим разбором authn.Verify — тем же,
//     которым gRPC-адаптер проверяет metadata authorization.
//  3. Отказ по форме или подписи — 401 Unauthorized, отсутствие пользователя в БД —
//     403 Forbidden, ошибка БД — 500 Internal server error.
//  4. В случае успеха кладёт user ID в контекст и передаёт запрос дальше.
//
// Применяется ко всем защищённым маршрутам в main.go.
func AuthMiddleware(db *sql.DB) func(http.Handler) http.Handler {
	users := authn.PostgresUsers{DB: db}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("user_id")
			if err != nil || cookie.Value == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			usrID, err := authn.Verify(r.Context(), cookie.Value, config.CookieKey, users)
			switch {
			case errors.Is(err, authn.ErrUnauthenticated):
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			case errors.Is(err, authn.ErrUserNotFound):
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			case err != nil:
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

// ShorterLoginPost обрабатывает POST-запросы на пути /login.
//
// Принимает JSON {"usr_name": "someName"}, ищет пользователя по usr_name в БД.
// Если не найден — создаёт нового (paswd пустой). Выдаёт HMAC-SHA256-подписанную куку
// с форматом "user_id:{payload}:{signature}", где payload — числовой ID пользователя.
// Кука HttpOnly, максимальный MaxAge — 30 дней.
//
// Пример запроса:
//
//	curl -X POST http://localhost:8080/login \
//	  -H "Content-Type: application/json" \
//	  -d '{"usr_name":"alice"}'
//
// Ответ 200:
//
//	{"user_id":"42"}
//
// + Set-Cookie: user_id=42:hmac_sha256_signature; Path=/; HttpOnly; Max-Age=2592000
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
		if err = json.Unmarshal(body, &req); err != nil || req.UsrName == "" {
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
		payload := strconv.Itoa(usrID)
		signature := authn.Sign(config.CookieKey, payload)

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

// ShorterUserURLsGet обрабатывает GET-запросы на пути /api/user/urls.
//
// Возвращает все сокращённые текущим пользователем URL из БД в виде JSON-массива.
// Требует авторизацию через AuthMiddleware.
// Если URL нет — возвращает 204 No Content.
//
// Пример запроса:
//
//	curl http://localhost:8080/api/user/urls -b "user_id=1:signature"
//
// Ответ 200:
//
//	[{"short_url":"http://short.ru/a1b2c3d4","original_url":"https://example.com/one"}]
func ShorterUserURLsGet(db *sql.DB) http.HandlerFunc {
	svc := &shorten.Service{URLs: shorten.PostgresUserURLReader{DB: db}}

	return func(w http.ResponseWriter, r *http.Request) {
		// извлекаем usrID из контекста (установлен AuthMiddleware)
		usrID, ok := r.Context().Value(contextKey{}).(int)
		if !ok {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		urls, err := svc.UserURLs(r.Context(), usrID)
		if err != nil {
			logrus.WithError(err).Error("Failed to query user URLs")
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		if len(urls) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(urls)
	}
}

// urlDeleter — мягкое удаление ссылок пользователя. Единственная операция, которую
// хендлер DeleteURLs выполняет сам; выделена в интерфейс, чтобы фоновую работу можно
// было проверить без настоящей PostgreSQL (тесты пакета работают в файловом режиме).
type urlDeleter interface {
	DeleteUrls(ctx context.Context, usrID int, urls []string) error
}

// DeleteURLs обрабатывает DELETE-запросы на пути /api/user/urls.
//
// Принимает JSON-массив short_url для удаления и запускает его в отдельной горутине,
// возвращая 202 Accepted сразу. Ответ означает, что удаление будет выполнено: горутина
// работает с контекстом процесса (lifecycle), а не с контекстом запроса (он отменяется
// вместе с ответом), и учитывается в wg, поэтому остановка сервиса её дождётся.
// Требует авторизацию через AuthMiddleware.
//
// Пример запроса:
//
//	curl -X DELETE http://localhost:8080/api/user/urls \
//	  -b "user_id=1:signature" \
//	  -H "Content-Type: application/json" \
//	  -d '["http://short.ru/a1b2c3d4","http://short.ru/e5f6g7h8"]'
//
// Ответ 202:
//
//	{"done":  "OK"}
func DeleteURLs(store urlDeleter, lifecycle context.Context, wg *sync.WaitGroup) http.HandlerFunc {
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

		wg.Go(func() {
			if err := store.DeleteUrls(lifecycle, usrID, forDel); err != nil {
				logrus.WithError(err).Errorf("Не выполнено удаление ссылок пользователя %d", usrID)
			}
		})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode("{\"done\":  \"OK\"}")

	}
}
