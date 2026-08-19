package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	models "study-go.ru/cho/eto/internal/model"
	"study-go.ru/cho/eto/internal/store"
	mock_store "study-go.ru/cho/eto/internal/store/mock"
)

const testUserID = "user-1"

// makeMessages создаёт n тестовых сообщений
func makeMessages(n int) []store.Message {
	msgs := make([]store.Message, n)
	for i := range msgs {
		msgs[i] = store.Message{ID: int64(i + 1), Payload: "message"}
	}
	return msgs
}

func TestWebhookApp(t *testing.T) {
	// body собирает валидный запрос SimpleUtterance с нужными параметрами сессии
	body := func(sessionID string, sessionNew bool) string {
		return fmt.Sprintf(
			`{"request": {"type": "SimpleUtterance", "command": "do something"}, "version": "1.0", `+
				`"session": {"id": %q, "new": %t, "user": {"user_id": %q}}, "timezone": "Europe/Moscow"}`,
			sessionID, sessionNew, testUserID,
		)
	}

	const (
		badJSONBody     = `{"request": }`
		unsupportedBody = `{"request": {"type": "idunno", "command": "do something"}, "version": "1.0"}`
		badTimezone     = "Not/AZone"
	)

	testCases := []struct {
		name                string
		method              string
		body                string
		storeCalled         bool
		storeErr            error
		messages            []store.Message
		expectedCode        int
		expectedText        string
		expectedTextPattern string
	}{
		{name: "GET: метод не поддерживается", method: http.MethodGet, expectedCode: http.StatusMethodNotAllowed},
		{name: "PUT: метод не поддерживается", method: http.MethodPut, expectedCode: http.StatusMethodNotAllowed},
		{name: "DELETE: метод не поддерживается", method: http.MethodDelete, expectedCode: http.StatusMethodNotAllowed},
		{name: "POST: пустое тело", method: http.MethodPost, expectedCode: http.StatusInternalServerError},
		{name: "POST: некорректный JSON", method: http.MethodPost, body: badJSONBody, expectedCode: http.StatusInternalServerError},
		{name: "POST: неизвестный тип запроса", method: http.MethodPost, body: unsupportedBody, expectedCode: http.StatusUnprocessableEntity},
		{
			name:         "POST: ошибка хранилища",
			method:       http.MethodPost,
			body:         body("id001", false),
			storeCalled:  true,
			storeErr:     errors.New("db unavailable"),
			expectedCode: http.StatusInternalServerError,
		},
		{
			name:         "POST: нет новых сообщений",
			method:       http.MethodPost,
			body:         body("id001", false),
			storeCalled:  true,
			expectedCode: http.StatusOK,
			expectedText: "Для вас нет новых сообщений.",
		},
		{
			name:         "POST: есть новые сообщения",
			method:       http.MethodPost,
			body:         body("id001", false),
			storeCalled:  true,
			messages:     makeMessages(3),
			expectedCode: http.StatusOK,
			expectedText: "Для вас 3 новых сообщений.",
		},
		{
			name:                "POST: новая сессия, валидный часовой пояс",
			method:              http.MethodPost,
			body:                body("id002", true),
			storeCalled:         true,
			expectedCode:        http.StatusOK,
			expectedTextPattern: `^Точное время \d+ часов, \d+ минут\. Для вас нет новых сообщений\.$`,
		},
		{
			name:                "POST: новая сессия с сообщениями, валидный часовой пояс",
			method:              http.MethodPost,
			body:                body("id002", true),
			storeCalled:         true,
			messages:            makeMessages(2),
			expectedCode:        http.StatusOK,
			expectedTextPattern: `^Точное время \d+ часов, \d+ минут\. Для вас 2 новых сообщений\.$`,
		},
		{
			name:         "POST: новая сессия, невалидный часовой пояс",
			method:       http.MethodPost,
			body:         strings.Replace(body("id002", true), "Europe/Moscow", badTimezone, 1),
			storeCalled:  true,
			expectedCode: http.StatusBadRequest,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ms := mock_store.NewMockMessageStore(ctrl)
			if tc.storeCalled {
				ms.EXPECT().ListMessages(gomock.Any(), testUserID).
					Return(tc.messages, tc.storeErr)
			}

			var bodyReader io.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, "/", bodyReader)
			w := httptest.NewRecorder()

			NewApp(ms).WebhookApp(w, req)

			assert.Equal(t, tc.expectedCode, w.Code)
			if tc.expectedText == "" && tc.expectedTextPattern == "" {
				return
			}

			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
			var resp models.Response
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			if tc.expectedTextPattern != "" {
				assert.Regexp(t, tc.expectedTextPattern, resp.Response.Text)
			} else {
				assert.Equal(t, tc.expectedText, resp.Response.Text)
			}
		})
	}
}
