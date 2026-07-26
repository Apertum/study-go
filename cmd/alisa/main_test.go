package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	alise "study-go.ru/cho/eto/internal/handler"
	models "study-go.ru/cho/eto/internal/model"
)

func TestWebhook(t *testing.T) {
	handler := http.HandlerFunc(alise.Webhook)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	successBody := `{
        "response": {
            "text": "Для вас нет новых сообщений."
        },
        "session": {"id":"", "new":false},
        "timezone":"Europe/Moscow",
        "version":"1.0"
    }`

	successBodyAsFirst := `{
            "response": {
                "text": "Точное время .* часов, .* минут. Для вас нет новых сообщений."
            },
            "session": {"id":"", "new":false},
            "timezone":"Europe/Moscow",
            "version":"1.0"
        }`

	testCases := []struct {
		name         string // добавляем название тестов
		method       string
		body         string // добавляем тело запроса в табличные тесты
		expectedCode int
		expectedBody string
	}{
		{
			name:         "method_get",
			method:       http.MethodGet,
			expectedCode: http.StatusMethodNotAllowed,
			expectedBody: "",
		},
		{
			name:         "method_put",
			method:       http.MethodPut,
			expectedCode: http.StatusMethodNotAllowed,
			expectedBody: "",
		},
		{
			name:         "method_delete",
			method:       http.MethodDelete,
			expectedCode: http.StatusMethodNotAllowed,
			expectedBody: "",
		},
		{
			name:         "method_post_without_body",
			method:       http.MethodPost,
			expectedCode: http.StatusInternalServerError,
			expectedBody: "",
		},
		{
			name:         "method_post_unsupported_type",
			method:       http.MethodPost,
			body:         `{"request": {"type": "idunno", "command": "do something"}, "version": "1.0"}`,
			expectedCode: http.StatusUnprocessableEntity,
			expectedBody: "",
		},
		{
			name:         "method_post_success",
			method:       http.MethodPost,
			body:         `{"request": {"type": "SimpleUtterance", "command": "sudo do something"}, "version": "1.0"}`,
			expectedCode: http.StatusOK,
			expectedBody: successBody,
		},
		{
			name:         "method_post_success",
			method:       http.MethodPost,
			body:         `{"request": {"type": "SimpleUtterance", "command": "sudo do something"}, "session": {"new": true, "id": "id002"}, "version": "1.0"}`,
			expectedCode: http.StatusOK,
			// ответ стал сложнее, поэтому сравниваем его с шаблоном вместо точной строки
			expectedBody: successBodyAsFirst,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.method, func(t *testing.T) {
			req := resty.New().R()
			req.Method = tc.method
			req.URL = srv.URL

			if len(tc.body) > 0 {
				req.SetHeader("Content-Type", "application/json")
				req.SetBody(tc.body)
			}

			resp, err := req.Send()
			assert.NoError(t, err, "error making HTTP request")

			assert.Equal(t, tc.expectedCode, resp.StatusCode(), "Response code didn't match expected")
			// проверяем корректность полученного тела ответа, если мы его ожидаем
			if tc.expectedBody != "" {
				//assert.JSONEq(t, tc.expectedBody, string(resp.Body()))
				// сравниваем тело ответа с ожидаемым шаблоном
				var rsExp models.Response
				var rsFact models.Response
				if err := json.Unmarshal([]byte(tc.expectedBody), &rsExp); err != nil {
					assert.Error(t, err, "Не Json expectedBody")
				}
				if err := json.Unmarshal(resp.Body(), &rsFact); err != nil {
					assert.Error(t, err, "Не Json resp.Body")
				}

				assert.Regexp(t, rsExp.Response.Text, rsFact.Response.Text)
			}
		})
	}
}

func TestGzipCompression(t *testing.T) {
	handler := http.HandlerFunc(gzipMiddlewareForTest(alise.Webhook))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	requestBody := `{
        "request": {
            "type": "SimpleUtterance",
            "command": "sudo do something"
        },
        "version": "1.0",
        "session": {"id":"", "new":false},
        "timezone":"Europe/Moscow"
    }`

	// ожидаемое содержимое тела ответа при успешном запросе
	successBody := `{
        "response": {
            "text": "Для вас нет новых сообщений."
        },
        "version": "1.0",
        "session": {"id":"", "new":false},
        "timezone":"Europe/Moscow"
    }`

	t.Run("sends_gzip", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		zb := gzip.NewWriter(buf)
		_, err := zb.Write([]byte(requestBody))
		require.NoError(t, err)
		err = zb.Close()
		require.NoError(t, err)

		r := httptest.NewRequest("POST", srv.URL, buf)
		r.RequestURI = ""
		r.Header.Set("Content-Encoding", "gzip")
		r.Header.Set("Accept-Encoding", "")

		resp, err := http.DefaultClient.Do(r)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		defer resp.Body.Close()

		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.JSONEq(t, successBody, string(b))
	})

	t.Run("accepts_gzip", func(t *testing.T) {
		buf := bytes.NewBufferString(requestBody)
		r := httptest.NewRequest("POST", srv.URL, buf)
		r.RequestURI = ""
		r.Header.Set("Accept-Encoding", "gzip")

		resp, err := http.DefaultClient.Do(r)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		defer resp.Body.Close()

		zr, err := gzip.NewReader(resp.Body)
		require.NoError(t, err)

		b, err := io.ReadAll(zr)
		require.NoError(t, err)

		require.JSONEq(t, successBody, string(b))
	})
}
