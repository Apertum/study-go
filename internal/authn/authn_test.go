package authn

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testKey = "cookie-key"

// fakeUsers — хранилище пользователей с заранее заданным ответом.
type fakeUsers struct {
	exists bool
	err    error
	called bool
	usrID  int
}

func (f *fakeUsers) UserExists(_ context.Context, usrID int) (bool, error) {
	f.called = true
	f.usrID = usrID

	return f.exists, f.err
}

func TestSign(t *testing.T) {
	t.Parallel()

	signature := Sign(testKey, "42")

	assert.Len(t, signature, 64)
	assert.Equal(t, signature, Sign(testKey, "42"), "подпись детерминирована")
	assert.NotEqual(t, signature, Sign(testKey, "43"))
	assert.NotEqual(t, signature, Sign("другой-ключ", "42"))
}

func TestVerify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     func() string
		users   *fakeUsers
		wantID  int
		wantErr error
	}{
		{
			name:   "корректные учётные данные",
			raw:    func() string { return credential("42", testKey) },
			users:  &fakeUsers{exists: true},
			wantID: 42,
		},
		{
			name:    "пустое значение",
			raw:     func() string { return "" },
			users:   &fakeUsers{exists: true},
			wantErr: ErrUnauthenticated,
		},
		{
			name:    "нет разделителя",
			raw:     func() string { return "42" },
			users:   &fakeUsers{exists: true},
			wantErr: ErrUnauthenticated,
		},
		{
			name:    "лишний разделитель",
			raw:     func() string { return credential("42", testKey) + ":extra" },
			users:   &fakeUsers{exists: true},
			wantErr: ErrUnauthenticated,
		},
		{
			name:    "подпись не совпала",
			raw:     func() string { return credential("42", "другой-ключ") },
			users:   &fakeUsers{exists: true},
			wantErr: ErrUnauthenticated,
		},
		{
			name:    "payload не число",
			raw:     func() string { return credential("abc", testKey) },
			users:   &fakeUsers{exists: true},
			wantErr: ErrUserNotFound,
		},
		{
			name:    "пользователя нет",
			raw:     func() string { return credential("42", testKey) },
			users:   &fakeUsers{},
			wantErr: ErrUserNotFound,
		},
		{
			name:    "ошибка хранилища",
			raw:     func() string { return credential("42", testKey) },
			users:   &fakeUsers{err: errors.New("база недоступна")},
			wantErr: ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Verify(context.Background(), tt.raw(), testKey, tt.users)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Zero(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantID, got)
		})
	}
}

func TestVerifyRejectsBeforeTouchingStorage(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "42", "42:a:b", credential("42", "чужой-ключ")} {
		users := &fakeUsers{exists: true}

		_, err := Verify(context.Background(), raw, testKey, users)
		require.ErrorIs(t, err, ErrUnauthenticated)
		assert.False(t, users.called, "отказ по учётным данным не должен идти в базу: %q", raw)
	}
}

// credential собирает значение учётных данных из payload по ключу key.
func credential(payload, key string) string {
	return payload + signatureSeparator + Sign(key, payload)
}
