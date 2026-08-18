package storage

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGet_FileMode проверяет семантику второго возвращаемого значения Get: это флаг «запись удалена»,
// а не «запись найдена». Регрессия: ранее ok из map выдавался как deleted,
// из-за чего ShorterGet отвечал 410 Gone вместо 307 на существующую ссылку.
func TestGet_FileMode(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "test_data.json"))

	require.NoError(t, s.PutUnique("1", "http://short.ru/abc", "https://example.com/long", 1))

	url, deleted, err := s.Get("http://short.ru/abc")
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/long", url)
	assert.False(t, deleted, "существующая запись не должна быть помечена удалённой")

	url, _, err = s.Get("http://short.ru/nonexistent")
	require.NoError(t, err)
	assert.Empty(t, url, "несуществующая запись должна отдавать пустой URL")
}
