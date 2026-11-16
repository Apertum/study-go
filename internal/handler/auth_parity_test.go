package handler

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"study-go.ru/cho/eto/internal/config"
	"study-go.ru/cho/eto/internal/middleware"
)

// authCookieValue собирает значение куки user_id с корректной подписью payload.
func authCookieValue(payload string) string {
	return payload + ":" + SigninCookieForTest(config.CookieKey, payload)
}

// TestAuthMiddleware_Refusals проверяет статусы отказа после того, как разбор учётных
// данных вынесен в authn.Verify: формы отказа были и раньше такими же.
func TestAuthMiddleware_Refusals(t *testing.T) {
	t.Parallel()

	config.CookieKey = "thisSuoerSecretMyKey"

	tests := []struct {
		name     string
		cookie   string
		wantCode int
		prepare  func(mock sqlmock.Sqlmock)
	}{
		{
			name:     "кука без разделителя",
			cookie:   "1",
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "payload не число при верной подписи",
			cookie:   authCookieValue("abc"),
			wantCode: http.StatusForbidden,
		},
		{
			name:     "сбой базы при проверке пользователя",
			cookie:   authCookieValue("1"),
			wantCode: http.StatusInternalServerError,
			prepare: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`SELECT 1 FROM usr WHERE id = \$1`).
					WithArgs(1).
					WillReturnError(sql.ErrConnDone)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db, mock := setupTestDB(t)
			defer db.Close()

			if tt.prepare != nil {
				tt.prepare(mock)
			}

			router := chi.NewRouter()
			router.Use(middleware.GzipMiddleware)
			router.Handle("/api/user/urls", AuthMiddleware(db)(ShorterUserURLsGet(db)))

			srv := httptest.NewServer(router)
			defer srv.Close()

			req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/user/urls", nil)
			require.NoError(t, err)
			req.AddCookie(&http.Cookie{Name: "user_id", Value: tt.cookie})

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.wantCode, resp.StatusCode)
		})
	}
}
