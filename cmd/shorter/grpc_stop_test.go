package main

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	shortenerv1 "study-go.ru/cho/eto/api/shortener/v1"
	"study-go.ru/cho/eto/internal/grpcapi"
	"study-go.ru/cho/eto/internal/shorten"
)

// Проверки stopGRPC — той фазы плана остановки, которая отпускает gRPC. Дозавершение начатого
// вызова и отказ новому проверяются здесь детерминированно: в собранном бинарнике метода,
// который можно задержать по команде, нет, а момент выбора соединения остановкой зависит от
// планировщика.

// hangStore — хранилище, чей Expand висит до команды теста: тот же эффект, что медленный
// запрос к базе в середине вызова. entered закрывается один раз: в тесте дренажа метода
// может коснуться и проверочный вызов.
type hangStore struct {
	enteredOnce sync.Once
	entered     chan struct{}
	release     chan struct{}
}

func (s *hangStore) NextID() string { return "1" }

func (s *hangStore) PutUnique(_, _, _ string, _ int) error { return nil }

func (s *hangStore) GetByOriginalURL(string) (string, bool) { return "", false }

func (s *hangStore) Get(string) (string, bool, error) {
	s.enteredOnce.Do(func() { close(s.entered) })
	<-s.release

	return "https://example.com/target", false, nil
}

// hangServer — gRPC-сервер с зависающим методом на bufconn и клиент к нему.
type hangServer struct {
	server *grpc.Server
	client shortenerv1.ShortenerServiceClient
}

func startHangServer(t *testing.T, store *hangStore) hangServer {
	t.Helper()

	listener := bufconn.Listen(1024 * 64)
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(
		grpcapi.UnaryRecover(),
		grpcapi.Auth("key", nil),
	))
	shortenerv1.RegisterShortenerServiceServer(server, grpcapi.New(&shorten.Service{Store: store}))

	go func() { _ = server.Serve(listener) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		server.Stop()
		_ = conn.Close()
		_ = listener.Close()
	})

	return hangServer{server: server, client: shortenerv1.NewShortenerServiceClient(conn)}
}

// expand запускает переход по ссылке в горутине и возвращает канал с его исходом.
func expand(client shortenerv1.ShortenerServiceClient) <-chan error {
	done := make(chan error, 1)

	go func() {
		_, err := client.ExpandURL(context.Background(), &shortenerv1.URLExpandRequest{Id: "abc"})
		done <- err
	}()

	return done
}

// TestStopGRPCFinishesInFlightCall — начатый вызов дозавершается и получает полный ответ,
// а остановка возвращается без ошибки: gRPC отпускается в группе слушателей, пока база и
// файл аудита ещё открыты.
func TestStopGRPCFinishesInFlightCall(t *testing.T) {
	store := &hangStore{entered: make(chan struct{}), release: make(chan struct{})}
	srv := startHangServer(t, store)

	inFlight := expand(srv.client)

	<-store.entered
	close(store.release)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	require.NoError(t, stopGRPC(ctx, srv.server))
	assert.NoError(t, <-inFlight)
}

// TestStopGRPCStopsHungCallAfterDeadline — зависший вызов не держит процесс: по истечении
// периода сервер обрывается принудительно, причиной остаётся истечение срока (по ней процесс
// завершается кодом 1), а новый вызов уже не обслуживается — UNAVAILABLE.
func TestStopGRPCStopsHungCallAfterDeadline(t *testing.T) {
	store := &hangStore{entered: make(chan struct{}), release: make(chan struct{})}
	srv := startHangServer(t, store)

	t.Cleanup(func() { close(store.release) })

	inFlight := expand(srv.client)

	<-store.entered

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stopped := make(chan error, 1)

	go func() { stopped <- stopGRPC(ctx, srv.server) }()

	// Дренаж начат: сервер принял начатый вызов и отказывает новым. Опрошивается, потому что
	// GOAWAY доходит до клиента за время хода планировщика.
	waitDraining(t, srv.client)

	select {
	case err := <-stopped:
		assert.ErrorIs(t, err, context.DeadlineExceeded, "зависший вызов должен быть прерван по сроку")
	case <-time.After(10 * time.Second):
		t.Fatal("stopGRPC не вернулся после истечения периода")
	}

	assert.NotEqual(t, codes.OK, status.Code(<-inFlight), "зависший вызов обязан быть прерван")
}

// waitDraining ждёт отказа новому вызову: UNAVAILABLE означает, что сервер перешёл в дренаж.
// Вызов с коротким сроком различает отказ сервера и отмену собственного ожидания.
func waitDraining(t *testing.T, client shortenerv1.ShortenerServiceClient) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		callCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)

		_, err := client.ExpandURL(callCtx, &shortenerv1.URLExpandRequest{Id: "abc"})
		cancel()

		if status.Code(err) == codes.Unavailable {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("новый вызов не получил отказ: %v", err)
		}

		time.Sleep(20 * time.Millisecond)
	}
}
