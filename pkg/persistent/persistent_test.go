// файл persistent/persistent_test.go

package persistent

import (
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	mock_store "study-go.ru/cho/eto/mocks"
)

func TestGet(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := mock_store.NewMockStore(ctrl)

	value := []byte("Value")
	m.EXPECT().
		Get(gomock.Any()).
		Return(value, nil).
		MaxTimes(5)

	for _, s := range []string{"Валерия", "Иван", "Екатерина"} {
		val, err := Lookup(m, s)
		require.NoError(t, err)
		require.Equal(t, val, value)
	}
}

func TestGetEmptyKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := mock_store.NewMockStore(ctrl)

	// возвращаемая ошибка
	errEmptyKey := errors.New("Указан пустой ключ")

	m.EXPECT().Get("").Return([]byte(""), errEmptyKey)
	_, err := Lookup(m, "")
	require.ErrorIs(t, err, errEmptyKey)
}
