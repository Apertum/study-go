package handler

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"study-go.ru/cho/eto/internal/config"
	"study-go.ru/cho/eto/internal/middleware"
	"study-go.ru/cho/eto/internal/storage"
)

func TestMain(m *testing.M) {
	// 1. Пытаемся удалить файл перед старте тестов
	err := os.Remove("test_data.json")

	// Если файла нет, os.Remove вернет ошибку os.ErrNotExist.
	// Игнорируем её, но логируем другие потенциальные проблемы (например, права доступа)
	if err != nil && !os.IsNotExist(err) {
		logrus.Printf("Не удалось удалить test_data.json при старте: %v", err)
	}

	// 2. Запускаем сами тесты пакета и сохраняем код возврата
	exitCode := m.Run()

	// 3. Завершаем процесс тестов с правильным кодом (0 - успех, >0 - падение)
	os.Exit(exitCode)
}

// тестовый middleware, который ставит usrID в контекст
func testAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), contextKey{}, 1)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// setupShorterRouter создаёт chi-роутер с зарегистрированными обработчиками shorter
func setupShorterRouter() http.Handler {
	return setupShorterRouterWithStorage(storage.New("test_data.json"))
}

// setupShorterRouterWithStorage создаёт chi-роутер с переданным storage
func setupShorterRouterWithStorage(s *storage.Storage) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.GzipMiddleware)
	r.Use(testAuthMiddleware)
	r.Post("/", ShorterPost(s))
	r.Get("/{id}", ShorterGet(s))
	return r
}

func TestShorterPostAndGet(t *testing.T) {
	router := setupShorterRouter()
	srv := httptest.NewServer(router)
	defer srv.Close()

	// тестовые URL для сокращения
	longURL := "https://practicum.yandex.ru/very/long/url/that/should/be/shortened/" + generateID()

	// POST — создание короткой ссылки
	t.Run("POST creates short link", func(t *testing.T) {
		resp, err := http.Post(srv.URL+"/", "text/plain", strings.NewReader(longURL))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var result map[string]string
		err = json.Unmarshal(body, &result)
		require.NoError(t, err)

		// проверяем, что ID сгенерирован и URL совпадает
		res, ok := result["short_url"]
		assert.True(t, ok, "expected 'result' field in response")
		assert.NotEmpty(t, res, "'result' should not be empty")

		url, ok := result["original_url"]
		assert.True(t, ok, "expected 'url' field in response")
		assert.Equal(t, longURL, url)

		// теперь GET по полученному ID — проверяем редирект
		t.Run("GET redirects to original URL", func(t *testing.T) {
			client := &http.Client{
				// не следуем редиректу — хотим проверить заголовок Location
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}

			getResp, err := client.Get(srv.URL + "/" + res)
			require.NoError(t, err)
			defer getResp.Body.Close()

			assert.Equal(t, http.StatusTemporaryRedirect, getResp.StatusCode)
			assert.Equal(t, longURL, getResp.Header.Get("Location"))
		})
	})
}

func TestShorterPostJSON(t *testing.T) {
	router := setupShorterRouter()
	srv := httptest.NewServer(router)
	defer srv.Close()

	var suff string = generateID()
	// POST с JSON
	jsonBody := `{"url":"https://example.com/from-json/` + suff + `"}`
	resp, err := http.Post(srv.URL+"/", "application/json", strings.NewReader(jsonBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var result map[string]string
	err = json.Unmarshal(body, &result)
	require.NoError(t, err)

	assert.NotEmpty(t, result["short_url"])
	assert.Equal(t, "https://example.com/from-json/"+suff, result["original_url"])
}

func TestShorterGetNotFound(t *testing.T) {
	router := setupShorterRouter()
	srv := httptest.NewServer(router)
	defer srv.Close()

	// GET для несуществующего ID
	resp, err := http.Get(srv.URL + "/nonexistent123")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestShorterPostEmptyBody(t *testing.T) {
	router := setupShorterRouter()
	srv := httptest.NewServer(router)
	defer srv.Close()

	// POST с пустым телом — проверяем, что сервер не падает
	resp, err := http.Post(srv.URL+"/", "text/plain", strings.NewReader(""))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var result map[string]string
	err = json.Unmarshal(body, &result)
	require.NoError(t, err)

	assert.NotEmpty(t, result["short_url"])
	// url должен быть пустым, т.к. body был пустой
	assert.Equal(t, "", result["original_url"])

	// Не уникальность
	resp2, err := http.Post(srv.URL+"/", "text/plain", strings.NewReader(""))
	require.NoError(t, err)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusConflict, resp2.StatusCode)
}

func TestShorterPostGzip(t *testing.T) {
	router := setupShorterRouter()
	srv := httptest.NewServer(router)
	defer srv.Close()

	// Сжимаем тело запроса в gzip
	longURL := "https://practicum.yandex.ru/very/long/url/that/should/be/shortened/" + generateID()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, err := gw.Write([]byte(longURL))
	require.NoError(t, err)
	err = gw.Close()
	require.NoError(t, err)

	// POST со сжатым телом и заголовком Content-Encoding: gzip
	req, err := http.NewRequest("POST", srv.URL+"/", &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var result map[string]string
	err = json.Unmarshal(body, &result)
	require.NoError(t, err)

	assert.NotEmpty(t, result["short_url"])
	assert.Equal(t, longURL, result["original_url"])
}

// TestShorterGetGzip проверяет редирект при запросе со сжатием
func TestShorterGetGzip(t *testing.T) {
	router := setupShorterRouter()
	srv := httptest.NewServer(router)
	defer srv.Close()

	longURL := "https://practicum.yandex.ru/very/long/url/that/should/be/shortened/" + generateID()

	// POST — создаём короткую ссылку
	resp, err := http.Post(srv.URL+"/", "text/plain", strings.NewReader(longURL))
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()

	var created map[string]string
	err = json.Unmarshal(body, &created)
	require.NoError(t, err)
	shortID := created["short_url"]

	// GET с заголовком Accept-Encoding: gzip — ждём сжатый ответ
	req, err := http.NewRequest("GET", srv.URL+"/"+shortID, nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	getResp, err := client.Do(req)
	require.NoError(t, err)
	defer getResp.Body.Close()

	// Ответ должен быть редиректом, а не сжатым контентом
	assert.Equal(t, http.StatusTemporaryRedirect, getResp.StatusCode)
	assert.Equal(t, longURL, getResp.Header.Get("Location"))
}

// TestShorterMultipleIDs проверяет, что каждый POST генерирует уникальный ID
func TestShorterMultipleIDs(t *testing.T) {
	router := setupShorterRouter()
	srv := httptest.NewServer(router)
	defer srv.Close()

	ids := make(map[string]struct{})

	for i := 0; i < 10; i++ {
		url := fmt.Sprintf("https://example.com/url-%d/%s", i, generateID())
		resp, err := http.Post(srv.URL+"/", "text/plain", strings.NewReader(url))
		require.NoError(t, err)

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result map[string]string
		json.Unmarshal(body, &result)
		res := result["short_url"]

		_, exists := ids[res]
		assert.False(t, exists, "ID %s уже используется", res)
		ids[res] = struct{}{}
	}
}

// setupTestDB создаёт *sql.DB с sqlmock
func setupTestDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	return db, mock
}

// TestShorterUserURLsGet_NoURLs проверяет 204 No Content когда у пользователя нет ссылок
func TestShorterUserURLsGet_NoURLs(t *testing.T) {
	db, mock := setupTestDB(t)
	defer db.Close()

	mock.ExpectQuery(`SELECT short_url, original_url FROM url_srv WHERE usr_id = \$1 ORDER BY id`).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"short_url", "original_url"}))

	handler := ShorterUserURLsGet(db)
	req := httptest.NewRequest("GET", "/api/user/urls", nil)
	ctx := context.WithValue(req.Context(), contextKey{}, 1)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())
}

// TestShorterUserURLsGet_WithURLs проверяет 200 OK с JSON-массивом URL
func TestShorterUserURLsGet_WithURLs(t *testing.T) {
	db, mock := setupTestDB(t)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"short_url", "original_url"}).
		AddRow("http://short.ru/abc123", "https://example.com/one").
		AddRow("http://short.ru/def456", "https://example.com/two")

	mock.ExpectQuery(`SELECT short_url, original_url FROM url_srv WHERE usr_id = \$1 ORDER BY id`).
		WithArgs(1).
		WillReturnRows(rows)

	handler := ShorterUserURLsGet(db)
	req := httptest.NewRequest("GET", "/api/user/urls", nil)
	ctx := context.WithValue(req.Context(), contextKey{}, 1)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var urls []struct {
		ShortURL    string `json:"short_url"`
		OriginalURL string `json:"original_url"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &urls)
	require.NoError(t, err)
	assert.Len(t, urls, 2)
	assert.Equal(t, "http://short.ru/abc123", urls[0].ShortURL)
	assert.Equal(t, "https://example.com/one", urls[0].OriginalURL)
	assert.Equal(t, "http://short.ru/def456", urls[1].ShortURL)
	assert.Equal(t, "https://example.com/two", urls[1].OriginalURL)
}

// TestShorterUserURLsGet_SingleURL проверяет один URL
func TestShorterUserURLsGet_SingleURL(t *testing.T) {
	db, mock := setupTestDB(t)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"short_url", "original_url"}).
		AddRow("http://short.ru/xyz789", "https://practicum.yandex.ru/very/long/url")

	mock.ExpectQuery(`SELECT short_url, original_url FROM url_srv WHERE usr_id = \$1 ORDER BY id`).
		WithArgs(1).
		WillReturnRows(rows)

	handler := ShorterUserURLsGet(db)
	req := httptest.NewRequest("GET", "/api/user/urls", nil)
	ctx := context.WithValue(req.Context(), contextKey{}, 1)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var urls []struct {
		ShortURL    string `json:"short_url"`
		OriginalURL string `json:"original_url"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &urls)
	require.NoError(t, err)
	assert.Len(t, urls, 1)
	assert.Equal(t, "http://short.ru/xyz789", urls[0].ShortURL)
	assert.Equal(t, "https://practicum.yandex.ru/very/long/url", urls[0].OriginalURL)
}

// TestShorterUserURLsGet_DBError проверяет обработку ошибки БД
func TestShorterUserURLsGet_DBError(t *testing.T) {
	db, mock := setupTestDB(t)
	defer db.Close()

	mock.ExpectQuery(`SELECT short_url, original_url FROM url_srv WHERE usr_id = \$1 ORDER BY id`).
		WithArgs(1).
		WillReturnError(fmt.Errorf("connection refused"))

	handler := ShorterUserURLsGet(db)
	req := httptest.NewRequest("GET", "/api/user/urls", nil)
	ctx := context.WithValue(req.Context(), contextKey{}, 1)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestShorterUserURLsGet_AuthMiddleware_NoCookie проверяет 401 при отсутствии куки
func TestShorterUserURLsGet_AuthMiddleware_NoCookie(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	r := chi.NewRouter()
	r.Use(middleware.GzipMiddleware)
	r.Handle("/api/user/urls", AuthMiddleware(db)(ShorterUserURLsGet(db)))

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/user/urls")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// TestShorterUserURLsGet_AuthMiddleware_BadSignature проверяет 401 при невалидной подписи
func TestShorterUserURLsGet_AuthMiddleware_BadSignature(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	r := chi.NewRouter()
	r.Use(middleware.GzipMiddleware)
	r.Handle("/api/user/urls", AuthMiddleware(db)(ShorterUserURLsGet(db)))

	srv := httptest.NewServer(r)
	defer srv.Close()

	// Кука с невалидной подписью (payload:"bad_signature")
	req, _ := http.NewRequest("GET", srv.URL+"/api/user/urls", nil)
	req.AddCookie(&http.Cookie{Name: "user_id", Value: "1:bad_signature"})

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// TestShorterUserURLsGet_AuthMiddleware_UserNotFound проверяет 403 при удалённом пользователе
func TestShorterUserURLsGet_AuthMiddleware_UserNotFound(t *testing.T) {
	db, mock := setupTestDB(t)
	defer db.Close()

	// AuthMiddleware делает SELECT 1 FROM usr WHERE id = 1 — возвращаем ErrNoRows
	mock.ExpectQuery(`SELECT 1 FROM usr WHERE id = \$1`).
		WithArgs(1).
		WillReturnError(sql.ErrNoRows) // вернёт 403 Forbidden

	r := chi.NewRouter()
	r.Use(middleware.GzipMiddleware)
	r.Handle("/api/user/urls", AuthMiddleware(db)(ShorterUserURLsGet(db)))

	srv := httptest.NewServer(r)
	defer srv.Close()

	// Генерируем правильную подпись для user_id=1
	config.CookieKey = "thisSuoerSecretMyKey"
	payload := "1"
	signature := SigninCookieForTest(config.CookieKey, payload)

	req, _ := http.NewRequest("GET", srv.URL+"/api/user/urls", nil)
	req.AddCookie(&http.Cookie{Name: "user_id", Value: payload + ":" + signature})

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}
