package grpcapi

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shortenerv1 "study-go.ru/cho/eto/api/shortener/v1"
	"study-go.ru/cho/eto/internal/shorten"
)

const (
	// privateURL — оригинальный URL запроса: он не должен попасть в журнал.
	privateURL = "https://private.example/payments?token=supersecret"
)

// TestUnaryRecoverIsolatesPanic проверяет, что паника обработчика превращается в INTERNAL,
// сервер продолжает обслуживать вызовы, а в журнале нет ни тела запроса, ни учётных данных.
//
// Приёмник журнала подменяется на весь процесс, поэтому тест не запускается параллельно.
func TestUnaryRecoverIsolatesPanic(t *testing.T) {
	logs := &bytes.Buffer{}

	previousOut := logrus.StandardLogger().Out
	previousLevel := logrus.GetLevel()
	logrus.SetOutput(logs)
	logrus.SetLevel(logrus.ErrorLevel)

	t.Cleanup(func() {
		logrus.SetOutput(previousOut)
		logrus.SetLevel(previousLevel)
	})

	store := &fakeStore{}
	store.setPanic("аварийное завершение записи")

	client := startServer(t, &shorten.Service{Store: store, BaseURL: testBase}, &fakeUsers{exists: true})
	ctx := callContext(t, testCredential("42"))

	_, err := client.ShortenURL(ctx, &shortenerv1.URLShortenRequest{Url: privateURL})
	require.Equal(t, codes.Internal, status.Code(err))

	// Сервер жив: тот же канал отвечает после восстановленной обработки.
	store.setPanic(nil)

	resp, err := client.ShortenURL(ctx, &shortenerv1.URLShortenRequest{Url: "https://example.com/after"})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(resp.GetResult(), testBase), "короткий URL собран из BaseURL: %s", resp.GetResult())

	written := logs.String()
	assert.Contains(t, written, "/shortener.v1.ShortenerService/ShortenURL")
	assert.NotContains(t, written, privateURL, "тело запроса в журнале")
	assert.NotContains(t, written, testCredential("42"), "учётные данные в журнале")
	assert.NotContains(t, written, testKey, "значение cookie_key в журнале")
}
