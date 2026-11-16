package shorten

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"study-go.ru/cho/eto/internal/audit"
)

// putCall — вызов сохранения, записанным фиктивным хранилищем.
type putCall struct {
	uuid        string
	shortURL    string
	originalURL string
	usrID       int
}

// fakeStore — хранилище с заранее заданными ответами.
type fakeStore struct {
	nextID      string
	putErr      error
	existing    string
	hasExisting bool
	originalURL string
	deleted     bool
	getErr      error

	puts []putCall
}

func (f *fakeStore) NextID() string {
	if f.nextID == "" {
		return "0"
	}

	return f.nextID
}

func (f *fakeStore) PutUnique(uuid, shortURL, originalURL string, usrID int) error {
	f.puts = append(f.puts, putCall{uuid: uuid, shortURL: shortURL, originalURL: originalURL, usrID: usrID})

	return f.putErr
}

func (f *fakeStore) GetByOriginalURL(string) (string, bool) {
	return f.existing, f.hasExisting
}

func (f *fakeStore) Get(string) (string, bool, error) {
	return f.originalURL, f.deleted, f.getErr
}

// fakeReader — список ссылок пользователя.
type fakeReader struct {
	urls    []Entry
	err     error
	gotUser int
}

func (r *fakeReader) List(_ context.Context, usrID int) ([]Entry, error) {
	r.gotUser = usrID

	return r.urls, r.err
}

// fakeAuditor — собранные события аудита.
type fakeAuditor struct {
	events []audit.Event
}

func (a *fakeAuditor) Notify(event audit.Event) {
	a.events = append(a.events, event)
}

func TestShorten_Success(t *testing.T) {
	t.Parallel()

	store := &fakeStore{nextID: "7"}
	auditor := &fakeAuditor{}
	svc := &Service{Store: store, Audit: auditor, BaseURL: "http://b/"}

	result, err := svc.Shorten(42, "https://example.com/a")
	require.NoError(t, err)

	assert.Equal(t, "7", result.UUID)
	assert.Equal(t, "https://example.com/a", result.OriginalURL)
	assert.Len(t, store.puts, 1)
	assert.Equal(t, "7", store.puts[0].uuid)
	assert.Equal(t, 42, store.puts[0].usrID)
	assert.Equal(t, "http://b/", store.puts[0].shortURL[:len("http://b/")])
	assert.Len(t, store.puts[0].shortURL, len("http://b/")+idBytes*2)

	require.Len(t, auditor.events, 1)
	assert.Equal(t, audit.ActionShorten, auditor.events[0].Action)
	assert.Equal(t, "42", auditor.events[0].UserID)
	assert.Equal(t, "https://example.com/a", auditor.events[0].URL)
	assert.Positive(t, auditor.events[0].Timestamp)
}

func TestShorten_Duplicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		store     *fakeStore
		wantError error
		wantShort string
	}{
		{
			name: "существующая ссылка возвращена",
			store: &fakeStore{
				putErr:      &pq.Error{Code: uniqueViolation},
				existing:    "http://b/old",
				hasExisting: true,
			},
			wantError: &DuplicateError{},
			wantShort: "http://b/old",
		},
		{
			name:      "дубликат без найденного оригинала — ошибка хранилища",
			store:     &fakeStore{putErr: &pq.Error{Code: uniqueViolation}},
			wantError: ErrStorage,
		},
		{
			name:      "прочая ошибка хранилища",
			store:     &fakeStore{putErr: errors.New("база недоступна")},
			wantError: ErrStorage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auditor := &fakeAuditor{}
			svc := &Service{Store: tt.store, Audit: auditor}

			_, err := svc.Shorten(1, "https://example.com/dup")

			var dup *DuplicateError
			if tt.wantShort != "" {
				require.ErrorAs(t, err, &dup)
				assert.Equal(t, tt.wantShort, dup.ShortURL)
				assert.Contains(t, dup.Error(), tt.wantShort)

				return
			}

			require.ErrorIs(t, err, tt.wantError)
			assert.Empty(t, auditor.events, "неуспешное сокращение не аудируется")
		})
	}
}

func TestExpand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		store      *fakeStore
		wantURL    string
		wantError  error
		wantEvents int
	}{
		{
			name:       "найдена",
			store:      &fakeStore{originalURL: "https://example.com/a"},
			wantURL:    "https://example.com/a",
			wantEvents: 1,
		},
		{name: "нет записи", store: &fakeStore{}, wantError: ErrNotFound},
		{name: "запись удалена", store: &fakeStore{originalURL: "https://a", deleted: true}, wantError: ErrGone},
		{name: "ошибка хранилища", store: &fakeStore{getErr: errors.New("оборвалось")}, wantError: ErrNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auditor := &fakeAuditor{}
			svc := &Service{Store: tt.store, Audit: auditor}

			got, err := svc.Expand("a1b2c3d4")

			if tt.wantError != nil {
				require.ErrorIs(t, err, tt.wantError)
				assert.Empty(t, got)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantURL, got)
			}

			assert.Len(t, auditor.events, tt.wantEvents)

			if tt.wantEvents == 1 {
				assert.Equal(t, audit.ActionFollow, auditor.events[0].Action)
				assert.Empty(t, auditor.events[0].UserID, "переход не привязан к пользователю")
			}
		})
	}
}

func TestUserURLs(t *testing.T) {
	t.Parallel()

	t.Run("успех", func(t *testing.T) {
		t.Parallel()

		urls := []Entry{{ShortURL: "http://b/a1", OriginalURL: "https://a"}}
		reader := &fakeReader{urls: urls}
		svc := &Service{URLs: reader}

		got, err := svc.UserURLs(context.Background(), 42)
		require.NoError(t, err)
		assert.Equal(t, urls, got)
		assert.Equal(t, 42, reader.gotUser)
	})

	t.Run("пустой список не ошибка", func(t *testing.T) {
		t.Parallel()

		svc := &Service{URLs: &fakeReader{}}

		got, err := svc.UserURLs(context.Background(), 1)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("ошибка чтения", func(t *testing.T) {
		t.Parallel()

		svc := &Service{URLs: &fakeReader{err: errors.New("база молчит")}}

		_, err := svc.UserURLs(context.Background(), 1)
		require.Error(t, err)
	})
}

func TestGenerateID(t *testing.T) {
	t.Parallel()

	first := GenerateID()
	second := GenerateID()

	assert.Len(t, first, idBytes*2)
	_, err := hex.DecodeString(first)
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
}

func TestIsDuplicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "нарушение уникальности", err: &pq.Error{Code: uniqueViolation}, want: true},
		{name: "обёрнутое нарушение", err: errors.Join(errors.New("слой"), &pq.Error{Code: uniqueViolation}), want: true},
		{name: "другой код postgres", err: &pq.Error{Code: "22001"}, want: false},
		{name: "не postgres", err: errors.New("обычная"), want: false},
		{name: "нет ошибки", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, isDuplicate(tt.err))
		})
	}
}
