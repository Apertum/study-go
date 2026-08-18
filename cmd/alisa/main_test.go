package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	alise "study-go.ru/cho/eto/internal/handler"
	models "study-go.ru/cho/eto/internal/model"
	mock_store "study-go.ru/cho/eto/internal/store/mock"
)

// simpleBody — валидное тело запроса для обработчика WebhookApp
const simpleBody = `{"request": {"type": "SimpleUtterance", "command": "do something"}, "version": "1.0"}`

// gzipBytes сжимает данные в gzip-формат
func gzipBytes(t *testing.T, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(data))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// gunzipString распаковывает gzip-данные в строку
func gunzipString(t *testing.T, data []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	require.NoError(t, err)
	return string(raw)
}

// newWebhookHandler собирает цепочку «gzipMiddleware + WebhookApp» с моком хранилища сообщений
func newWebhookHandler(t *testing.T) http.Handler {
	t.Helper()
	ctrl := gomock.NewController(t)
	ms := mock_store.NewMockMessageStore(ctrl)
	ms.EXPECT().ListMessages(gomock.Any(), gomock.Any()).
		Return(nil, nil).
		AnyTimes()
	h := alise.NewApp(ms)
	return gzipMiddleware(http.HandlerFunc(h.WebhookApp))
}

func TestGzipMiddleware(t *testing.T) {
	testCases := []struct {
		name            string
		requestBody     []byte
		contentEncoding string
		acceptEncoding  string
		expectedCode    int
		expectGzipResp  bool
	}{
		{name: "без сжатия", requestBody: []byte(simpleBody), expectedCode: http.StatusOK},
		{name: "сжатое тело запроса", requestBody: gzipBytes(t, simpleBody), contentEncoding: "gzip", expectedCode: http.StatusOK},
		{name: "сжатый ответ", requestBody: []byte(simpleBody), acceptEncoding: "gzip", expectedCode: http.StatusOK, expectGzipResp: true},
		{name: "сжатый запрос и ответ", requestBody: gzipBytes(t, simpleBody), contentEncoding: "gzip", acceptEncoding: "gzip", expectedCode: http.StatusOK, expectGzipResp: true},
		{name: "битое gzip-тело запроса", requestBody: []byte("not gzip data"), contentEncoding: "gzip", expectedCode: http.StatusInternalServerError},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(tc.requestBody))
			if tc.contentEncoding != "" {
				req.Header.Set("Content-Encoding", tc.contentEncoding)
			}
			if tc.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", tc.acceptEncoding)
			}

			w := httptest.NewRecorder()
			newWebhookHandler(t).ServeHTTP(w, req)

			assert.Equal(t, tc.expectedCode, w.Code)
			if tc.expectedCode != http.StatusOK {
				return
			}

			var resp models.Response
			if tc.expectGzipResp {
				require.NoError(t, json.Unmarshal([]byte(gunzipString(t, w.Body.Bytes())), &resp))
			} else {
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			}
			assert.NotEmpty(t, resp.Response.Text)
		})
	}
}

func TestTimerTrace(t *testing.T) {
	served := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("ok"))
	})

	w := httptest.NewRecorder()
	TimerTrace(next).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.True(t, served, "TimerTrace должен передать запрос следующему обработчику")
	assert.Equal(t, http.StatusTeapot, w.Code)
	assert.Equal(t, "ok", w.Body.String())
}

func TestCompressWriter(t *testing.T) {
	t.Run("WriteHeader: успешный статус ставит Content-Encoding", func(t *testing.T) {
		w := httptest.NewRecorder()
		cw := newCompressWriter(w)

		cw.WriteHeader(http.StatusOK)

		assert.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("WriteHeader: ошибочный статус не ставит Content-Encoding", func(t *testing.T) {
		w := httptest.NewRecorder()
		cw := newCompressWriter(w)

		cw.WriteHeader(http.StatusInternalServerError)

		assert.Empty(t, w.Header().Get("Content-Encoding"))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("Write сжимает данные", func(t *testing.T) {
		w := httptest.NewRecorder()
		cw := newCompressWriter(w)

		n, err := cw.Write([]byte(`{"text":"hello"}`))
		require.NoError(t, err)
		assert.Equal(t, len(`{"text":"hello"}`), n)
		require.NoError(t, cw.Close())

		assert.Equal(t, `{"text":"hello"}`, gunzipString(t, w.Body.Bytes()))
	})
}

func TestCompressReader(t *testing.T) {
	t.Run("Read распаковывает тело запроса", func(t *testing.T) {
		cr, err := newCompressReader(io.NopCloser(bytes.NewReader(gzipBytes(t, "hello gzip"))))
		require.NoError(t, err)

		raw, err := io.ReadAll(cr)
		require.NoError(t, err)
		assert.Equal(t, "hello gzip", string(raw))
		require.NoError(t, cr.Close())
	})

	t.Run("некорректные gzip-данные возвращают ошибку", func(t *testing.T) {
		_, err := newCompressReader(io.NopCloser(strings.NewReader("not gzip data")))
		assert.Error(t, err)
	})

	t.Run("Close возвращает ошибку закрытия тела запроса", func(t *testing.T) {
		errClose := errors.New("close failed")
		er := errCloseReader{rc: bytes.NewReader(gzipBytes(t, "data")), closeErr: errClose}
		cr, err := newCompressReader(er)
		require.NoError(t, err)

		_, err = io.ReadAll(cr)
		require.NoError(t, err)
		assert.ErrorIs(t, cr.Close(), errClose)
	})
}

// errCloseReader — io.ReadCloser, чей Close возвращает заданную ошибку
type errCloseReader struct {
	rc       io.Reader
	closeErr error
}

func (e errCloseReader) Read(p []byte) (int, error) {
	return e.rc.Read(p)
}

func (e errCloseReader) Close() error {
	return e.closeErr
}
