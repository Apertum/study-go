package handler

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"study-go.ru/cho/eto/internal/middleware"
	"study-go.ru/cho/eto/internal/storage"
)

// setupShorterRouter создаёт chi-роутер с зарегистрированными обработчиками shorter
func setupShorterRouter() http.Handler {
	return setupShorterRouterWithStorage(storage.New("test_data.json"))
}

// setupShorterRouterWithStorage создаёт chi-роутер с переданным storage
func setupShorterRouterWithStorage(s *storage.Storage) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.GzipMiddleware)
	r.Post("/", ShorterPost(s))
	r.Get("/{id}", ShorterGet(s))
	return r
}

func TestShorterPostAndGet(t *testing.T) {
	router := setupShorterRouter()
	srv := httptest.NewServer(router)
	defer srv.Close()

	// тестовые URL для сокращения
	longURL := "https://practicum.yandex.ru/very/long/url/that/should/be/shortened"

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

	// POST с JSON
	jsonBody := `{"url":"https://example.com/from-json"}`
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
	assert.Equal(t, "https://example.com/from-json", result["original_url"])
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
}

func TestShorterPostGzip(t *testing.T) {
	router := setupShorterRouter()
	srv := httptest.NewServer(router)
	defer srv.Close()

	// Сжимаем тело запроса в gzip
	longURL := "https://practicum.yandex.ru/very/long/url/that/should/be/shortened"
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

	longURL := "https://practicum.yandex.ru/very/long/url/that/should/be/shortened"

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
		url := fmt.Sprintf("https://example.com/url-%d", i)
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
