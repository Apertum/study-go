package authn

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresUsers_UserExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prepare   func(mock sqlmock.Sqlmock)
		wantExist bool
		wantError bool
	}{
		{
			name: "пользователь есть",
			prepare: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(userExistsQuery)).WithArgs(42).
					WillReturnRows(sqlmock.NewRows([]string{"one"}).AddRow(1))
			},
			wantExist: true,
		},
		{
			name: "строки нет — это не ошибка",
			prepare: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(userExistsQuery)).WithArgs(42).
					WillReturnError(sql.ErrNoRows)
			},
		},
		{
			name: "сбой базы",
			prepare: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(userExistsQuery)).WithArgs(42).WillReturnError(sql.ErrConnDone)
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			tt.prepare(mock)

			got, err := PostgresUsers{DB: db}.UserExists(context.Background(), 42)

			if tt.wantError {
				require.Error(t, err)
				assert.False(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantExist, got)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
