package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	shortenerv1 "study-go.ru/cho/eto/api/shortener/v1"
)

// Сквозные проверки gRPC-слушателя на собранном бинарнике: адрес из настройки, открытый текст
// без конфигурации TLS, независимость от ENABLE_HTTPS и участие в плане остановки.
//
// Дозавершение начатого вызова и отказ новому проверено детерминированно на bufconn
// (TestStopGRPCFinishesInFlightCall и TestStopGRPCStopsHungCallAfterDeadline в этом пакете):
// в собранном процессе нет метода, который можно задержать по команде, а момент, когда
// остановка успеет выбрать соединение, зависит от планировщика.

// grpcClient возвращает клиента собранного сервера. Транспорт — открытый текст: TLS gRPC не
// использует независимо от ENABLE_HTTPS.
func grpcClient(t *testing.T, addr string) shortenerv1.ShortenerServiceClient {
	t.Helper()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return shortenerv1.NewShortenerServiceClient(conn)
}

// waitGRPCServed ждёт, пока адрес начнёт отвечать: отказ авторизации на вызове без учётных
// данных означает «сервер поднялся», соединение без слушателя отвечает UNAVAILABLE.
func waitGRPCServed(t *testing.T, addr string, proc *shorterProcess) shortenerv1.ShortenerServiceClient {
	t.Helper()

	client := grpcClient(t, addr)
	deadline := time.Now().Add(30 * time.Second)

	for {
		_, err := client.ListUserURLs(context.Background(), &emptypb.Empty{})

		if status.Code(err) == codes.Unauthenticated {
			return client
		}

		if time.Now().After(deadline) {
			t.Fatalf("gRPC %s не ответил штатно: %v\nвывод:\n%s", addr, err, proc.output.String())
		}

		time.Sleep(50 * time.Millisecond)
	}
}

// TestGRPCServerOnConfiguredAddressStopsOnSIGTERM — адрес взят из GRPC_ADDRESS, вызов работает
// без TLS, событие аудита от gRPC-вызова попало в тот же файл, SIGTERM завершает процесс нулём
// и отпускает оба адреса.
func TestGRPCServerOnConfiguredAddressStopsOnSIGTERM(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildShorter(t)
	dir := t.TempDir()
	addr := freeAddr(t)
	grpcAddr := freeAddr(t)
	auditPath := filepath.Join(dir, "audit.jsonl")

	seedStore(t, dir)

	env := append(fileModeEnv(addr, dir, auditPath), "GRPC_ADDRESS="+grpcAddr)

	proc := startShorter(t, bin, dir, env)

	waitServed(t, addr, proc)

	client := waitGRPCServed(t, grpcAddr, proc)

	// Переход по ссылке публичен: учётные данные не нужны, как у GET /{id}.
	resp, err := client.ExpandURL(context.Background(), &shortenerv1.URLExpandRequest{Id: "abc"})
	require.NoError(t, err, "вывод:\n%s", proc.output.String())
	assert.Equal(t, "https://example.com/target", resp.GetResult())

	_, err = client.ListUserURLs(context.Background(), &emptypb.Empty{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// Метод сокращения тоже доступен по настроенному адресу: отказ наступил на авторизации,
	// а не на «сервис не зарегистрирован» (Unimplemented).
	_, err = client.ShortenURL(context.Background(), &shortenerv1.URLShortenRequest{Url: "https://example.com/a"})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// HTTPS выключен: gRPC работает, а материалы TLS никто не выпускал.
	for _, name := range []string{"cert.pem", "key.pem"} {
		assert.NoFileExists(t, filepath.Join(dir, name), "при выключенном HTTPS появился файл %s", name)
	}

	proc.stopBySignal(t, syscall.SIGTERM)

	require.Equal(t, 0, proc.exitCode(t), "остановка с gRPC-слушателем обязана завершаться кодом 0; вывод:\n%s", proc.output.String())

	assert.False(t, acceptsConn(addr), "адрес %s занят после завершения процесса", addr)
	assert.False(t, acceptsConn(grpcAddr), "gRPC-адрес %s занят после завершения процесса", grpcAddr)

	lines := strings.Join(nonEmptyLines(t, auditPath), "\n")
	assert.Contains(t, lines, string(`"action":"follow"`), "событие аудита от gRPC-вызова не дошло до файла")
	assert.Contains(t, lines, "https://example.com/target")
}

// TestGRPCAddressConflictRefusesStart — gRPC не может занять адрес основного слушателя: процесс
// завершается ошибкой до прослушивания портов, назвав оба способа задания.
func TestGRPCAddressConflictRefusesStart(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildShorter(t)
	dir := t.TempDir()
	addr := freeAddr(t)
	auditPath := filepath.Join(dir, "audit.jsonl")

	env := append(fileModeEnv(addr, dir, auditPath), "GRPC_ADDRESS="+addr)

	proc := startShorter(t, bin, dir, env)

	code := proc.exitCode(t)

	assert.NotEqual(t, 0, code, "совпадение адресов не остановило запуск; вывод:\n%s", proc.output.String())
	assert.Contains(t, proc.output.String(), "совпадает")
	assert.False(t, acceptsConn(addr), "адрес %s занят процессом, завершившимся ошибкой", addr)

	// Ошибка конфигурации не должна выглядеть как обслуженный запуск: слушатели не подняты.
	_, err := os.Stat(auditPath)
	if err == nil {
		lines := nonEmptyLines(t, auditPath)
		assert.Empty(t, lines, "в файл аудита что-то записано до отказа конфигурации")
	} else {
		assert.True(t, os.IsNotExist(err), "файл аудита не создаётся до прослушивания портов: %v", err)
	}
}

// TestGRPCUnaffectedByHTTPS — включённый HTTPS меняет состав HTTP-слушателей, но не транспорт
// gRPC: канал без TLS конфигурации работает, пока основной адрес обслуживает TLS.
func TestGRPCUnaffectedByHTTPS(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildShorter(t)
	dir := t.TempDir()
	tlsAddr := freeAddr(t)
	redirectAddr := freeAddr(t)
	grpcAddr := freeAddr(t)
	auditPath := filepath.Join(dir, "audit.jsonl")

	seedStore(t, dir)

	env := append(fileModeEnv(tlsAddr, dir, auditPath),
		"ENABLE_HTTPS=true",
		"HTTP_SERVER_ADDRESS="+redirectAddr,
		"CERT_FILE="+filepath.Join(dir, "cert.pem"),
		"KEY_FILE="+filepath.Join(dir, "key.pem"),
		"GRPC_ADDRESS="+grpcAddr,
	)

	proc := startShorter(t, bin, dir, env)

	waitServed(t, tlsAddr, proc)

	client := waitGRPCServed(t, grpcAddr, proc)

	resp, err := client.ExpandURL(context.Background(), &shortenerv1.URLExpandRequest{Id: "abc"})
	require.NoError(t, err, "вывод:\n%s", proc.output.String())
	assert.Equal(t, "https://example.com/target", resp.GetResult())

	// Материалы TLS остались у HTTPS-ветви: gRPC их не требует и не трогает.
	assert.FileExists(t, filepath.Join(dir, "cert.pem"))

	proc.stopBySignal(t, syscall.SIGTERM)

	assert.Equal(t, 0, proc.exitCode(t), "вывод:\n%s", proc.output.String())
}
