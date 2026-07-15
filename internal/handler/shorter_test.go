package handler

import (
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
)

// setupShorterRouter создаёт chi-роутер с зарегистрированными обработчиками shorter
func setupShorterRouter() http.Handler {
	r := chi.NewRouter()
	r.Post("/", ShorterPost)
	r.Get("/{id}", ShorterGet)
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

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var result map[string]string
		err = json.Unmarshal(body, &result)
		require.NoError(t, err)

		// проверяем, что ID сгенерирован и URL совпадает
		res, ok := result["result"]
		assert.True(t, ok, "expected 'result' field in response")
		assert.NotEmpty(t, res, "'result' should not be empty")

		url, ok := result["url"]
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

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var result map[string]string
	err = json.Unmarshal(body, &result)
	require.NoError(t, err)

	assert.NotEmpty(t, result["result"])
	assert.Equal(t, "https://example.com/from-json", result["url"])
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

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var result map[string]string
	err = json.Unmarshal(body, &result)
	require.NoError(t, err)

	assert.NotEmpty(t, result["result"])
	// url должен быть пустым, т.к. body был пустой
	assert.Equal(t, "", result["url"])
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
		res := result["result"]

		_, exists := ids[res]
		assert.False(t, exists, "ID %s уже используется", res)
		ids[res] = struct{}{}
	}
}
