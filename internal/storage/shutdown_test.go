package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeleteUrlsFileModeReturnsError закрывает nil-deref: в файловом режиме мягкого
// удаления нет, и об этом сообщает ошибка вместо паники в горутине хендлера.
func TestDeleteUrlsFileModeReturnsError(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "db-less.json"))

	require.NoError(t, s.PutUnique("1", "http://short.ru/abc", "https://example.com/a", 1))

	err := s.DeleteUrls(context.Background(), 1, []string{"http://short.ru/abc"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "файловом режиме")
}

// TestCloseFileModeIsNoop: в файловом режиме закрывать нечего, повторный вызов безопасен,
// данные после него остаются доступны из памяти.
func TestCloseFileModeIsNoop(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "db-less.json"))

	require.NoError(t, s.PutUnique("1", "http://short.ru/abc", "https://example.com/a", 1))

	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "повторный Close не должен возвращать ошибку")

	url, deleted, err := s.Get("http://short.ru/abc")
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/a", url)
	assert.False(t, deleted)
}

// dbBackedStorage собирает хранилище с пулом PostgreSQL, который заведомо не соединяется:
// sql.Open ленив, поэтому поведение закрытия проверяется без сервера БД.
func dbBackedStorage(t *testing.T) *Storage {
	t.Helper()

	db, err := sql.Open("postgres", "host=127.0.0.1 port=1 user=shorter dbname=shorter sslmode=disable connect_timeout=1")
	require.NoError(t, err)

	return &Storage{
		db:       db,
		useDB:    true,
		store:    map[string]string{"http://short.ru/abc": "https://example.com/a"},
		ids:      map[string]string{"1": "http://short.ru/abc"},
		urlUsers: map[string]int{"http://short.ru/abc": 1},
	}
}

// TestClosePoolsIsIdempotent: остановка может вызвать Close повторно (например, упал
// слушатель, и фаза прошла дважды) — второй вызов не ошибка.
func TestClosePoolsIsIdempotent(t *testing.T) {
	s := dbBackedStorage(t)

	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "повторный Close пула не должен возвращать ошибку")
}

// TestReadsAfterCloseFailWithoutPanic: Close намеренно не обнуляет указатель на пул —
// запрос, дошедший после остановки, обязан получить ошибку, а не панику.
func TestReadsAfterCloseFailWithoutPanic(t *testing.T) {
	s := dbBackedStorage(t)

	require.NoError(t, s.Close())

	_, _, err := s.Get("http://short.ru/abc")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "closed")

	require.Error(t, s.DeleteUrls(context.Background(), 1, []string{"http://short.ru/abc"}))
}
