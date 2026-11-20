package shorten

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresUserURLReader_List(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prepare   func(mock sqlmock.Sqlmock)
		wantURLs  []Entry
		wantError bool
	}{
		{
			name: "несколько ссылок",
			prepare: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"short_url", "original_url"}).
					AddRow("http://b/a1", "https://one").
					AddRow("http://b/b2", "https://two")
				mock.ExpectQuery(regexp.QuoteMeta(userURLsQuery)).WithArgs(42).WillReturnRows(rows)
			},
			wantURLs: []Entry{{ShortURL: "http://b/a1", OriginalURL: "https://one"}, {ShortURL: "http://b/b2", OriginalURL: "https://two"}},
		},
		{
			name: "пустой список",
			prepare: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(userURLsQuery)).WithArgs(42).
					WillReturnRows(sqlmock.NewRows([]string{"short_url", "original_url"}))
			},
		},
		{
			name: "ошибка запроса",
			prepare: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(userURLsQuery)).WithArgs(42).WillReturnError(sql.ErrConnDone)
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

			got, err := PostgresUserURLReader{DB: db}.List(context.Background(), 42)

			if tt.wantError {
				require.ErrorIs(t, err, ErrStorage)
				assert.Nil(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantURLs, got)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPostgresUserURLReader_ListErrorInRows(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	rows := sqlmock.NewRows([]string{"short_url", "original_url"}).
		AddRow("http://b/a1", "https://one").
		RowError(0, sql.ErrConnDone)
	mock.ExpectQuery(regexp.QuoteMeta(userURLsQuery)).WithArgs(1).WillReturnRows(rows)

	_, err = PostgresUserURLReader{DB: db}.List(context.Background(), 1)
	require.ErrorIs(t, err, ErrStorage)
}
