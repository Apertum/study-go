package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"study-go.ru/cho/eto/internal/audit"
	config "study-go.ru/cho/eto/internal/config"
)

// Остановка проверяется на уровне serve и shutdown: сигналы здесь эмулируются отменой
// контекста, а доставка настоящего SIGTERM — в shutdown_integration_test.go.

// freeAddr возвращает заведомо свободный адрес на loopback.
func freeAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	return addr
}

// withTestServerConfig задаёт настройки на время теста: основной адрес всегда свободный,
// при https добавляются адрес редиректа и пути до материалов.
func withTestServerConfig(t *testing.T, https bool) (string, string) {
	t.Helper()

	prevAddr, prevRedirect := config.Addr, config.HTTPRedirectAddr
	prevHTTPS, prevCert, prevKey := config.EnableHTTPS, config.CertFile, config.KeyFile

	t.Cleanup(func() {
		config.Addr, config.HTTPRedirectAddr = prevAddr, prevRedirect
		config.EnableHTTPS, config.CertFile, config.KeyFile = prevHTTPS, prevCert, prevKey
	})

	addr := freeAddr(t)
	config.Addr, config.EnableHTTPS = addr, https

	if !https {
		return addr, ""
	}

	redirectAddr := freeAddr(t)
	config.HTTPRedirectAddr = redirectAddr
	config.CertFile = filepath.Join(t.TempDir(), "cert.pem")
	config.KeyFile = filepath.Join(t.TempDir(), "key.pem")

	return addr, redirectAddr
}

// acceptsConn проверяет, что на адресе принимают соединения.
func acceptsConn(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}

	_ = conn.Close()

	return true
}

// waitServing дожидается, пока на адресе начнут принимать соединения.
func waitServing(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		if acceptsConn(addr) {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("на %s так и не начали принимать соединения", addr)
}

// waitRefusing дожидается, пока на адресе перестанут принимать соединения: слушатель
// отпускается в самом начале фазы остановки.
func waitRefusing(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		if !acceptsConn(addr) {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("на %s по-прежнему принимают соединения", addr)
}

// quietServer поднимает на свободном адресе сервер с мгновенным ответом.
func quietServer(t *testing.T, body string) (*http.Server, string) {
	t.Helper()

	addr := freeAddr(t)

	server := &http.Server{
		Addr:              addr,
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }),
		ReadHeaderTimeout: listenTimeout,
	}

	ln, err := net.Listen("tcp", addr)
	require.NoError(t, err)

	go func() { _ = server.Serve(ln) }()

	waitServing(t, addr)

	t.Cleanup(func() { _ = server.Close() })

	return server, addr
}

// hangingServer поднимает сервер, обработчик которого получает запрос и висит до открытия
// шлюза: им проверяют, что остановка ждёт начатый запрос и что фаза не наступает раньше времени.
func hangingServer(t *testing.T) (*http.Server, string) {
	t.Helper()

	addr := freeAddr(t)

	release := make(chan struct{})
	entered := make(chan struct{}, 1)

	server := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			entered <- struct{}{}
			<-release
			w.WriteHeader(http.StatusTeapot)
		}),
		ReadHeaderTimeout: listenTimeout,
	}

	ln, err := net.Listen("tcp", addr)
	require.NoError(t, err)

	go func() { _ = server.Serve(ln) }()

	waitServing(t, addr)

	go func() {
		resp, err := http.Get("http://" + addr + "/hanging") //nolint:noctx // тестовый запрос живёт до конца теста
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("обработчик не получил запрос")
	}

	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}

		_ = server.Close()
	})

	return server, addr
}

// spyCloser пишет порядок закрытия и может проверить состояние остальных фаз в свой момент.
type spyCloser struct {
	mu      *sync.Mutex
	order   *[]string
	atClose func()
	err     error
	name    string
}

func (c *spyCloser) Close() error {
	c.mu.Lock()
	*c.order = append(*c.order, c.name)
	c.mu.Unlock()

	if c.atClose != nil {
		c.atClose()
	}

	return c.err
}

func (c *spyCloser) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]string(nil), *c.order...)
}

// gateSubscriber — подписчик аудита, который не разбирает события до открытия шлюза.
type gateSubscriber struct {
	release   chan struct{}
	mu        sync.Mutex
	processed int
}

func (s *gateSubscriber) Handle(audit.Event) {
	<-s.release

	s.mu.Lock()
	defer s.mu.Unlock()
	s.processed++
}

func (s *gateSubscriber) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.processed
}

// TestServeReleasesAddressOnCancel: по отмене контекста serve дозавершает запросы,
// отпускает адрес и возвращает nil. Возврат из serve означает «ресурсы освобождены».
func TestServeReleasesAddressOnCancel(t *testing.T) {
	addr, _ := withTestServerConfig(t, false)

	ctx, cancel := context.WithCancel(context.Background())

	stopped := make(chan error, 1)

	go func() { stopped <- serve(ctx, http.NotFoundHandler(), stopPlan{timeout: 5 * time.Second}) }()

	waitServing(t, addr)
	cancel()

	select {
	case err := <-stopped:
		require.NoError(t, err, "чистая остановка не должна возвращать ошибку")
	case <-time.After(10 * time.Second):
		t.Fatal("serve не вернулся после отмены контекста")
	}

	// Слушатель отпущен: повторный bind того же адреса проходит.
	listener, err := net.Listen("tcp", addr)
	require.NoError(t, err, "адрес не освобождён после остановки")
	require.NoError(t, listener.Close())
}

// TestServeOccupiedAddressFailsSync проверяет, что ошибка занятого адреса возвращается
// синхронно: неверная конфигурация не должна выглядеть как работающий сервер.
func TestServeOccupiedAddressFailsSync(t *testing.T) {
	addr, _ := withTestServerConfig(t, false)

	occupant, err := net.Listen("tcp", addr)
	require.NoError(t, err)

	t.Cleanup(func() { _ = occupant.Close() })

	done := make(chan error, 1)

	go func() { done <- serve(context.Background(), http.NotFoundHandler(), stopPlan{timeout: time.Second}) }()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), addr)
	case <-time.After(5 * time.Second):
		t.Fatal("serve не вернулся с ошибкой занятого адреса")
	}
}

// TestServeCompletesInFlightRequest: запрос, начатый до сигнала, получает полный ответ —
// обработчик завершается уже после отмены контекста.
func TestServeCompletesInFlightRequest(t *testing.T) {
	addr, _ := withTestServerConfig(t, false)

	release := make(chan struct{})
	entered := make(chan struct{}, 1)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release

		_, _ = io.WriteString(w, "done")
	})

	ctx, cancel := context.WithCancel(context.Background())

	stopped := make(chan error, 1)

	go func() { stopped <- serve(ctx, handler, stopPlan{timeout: 10 * time.Second}) }()

	waitServing(t, addr)

	// Ответ читается целиком в горутине клиента: проверяется именно «дослали до конца».
	type served struct {
		body string
		code int
	}

	response := make(chan served, 1)
	failure := make(chan error, 1)

	go func() {
		resp, err := http.Get("http://" + addr + "/dolgo") //nolint:noctx // запрос должен пережить сигнал остановки
		if err != nil {
			failure <- err

			return
		}

		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			failure <- err

			return
		}

		response <- served{code: resp.StatusCode, body: string(body)}
	}()

	<-entered
	cancel()
	close(release)

	select {
	case err := <-failure:
		t.Fatalf("начатый запрос прерван: %v", err)
	case got := <-response:
		assert.Equal(t, http.StatusOK, got.code)
		assert.Equal(t, "done", got.body)
	case <-time.After(10 * time.Second):
		t.Fatal("начатый запрос не получил ответа")
	}

	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve не вернулся после дозавершения запроса")
	}
}

// TestHTTPSConflictingAddressesFailWithoutBind сохраняет прежнее поведение: конфликт
// основного адреса и адреса редиректа виден до прослушивания портов.
func TestHTTPSConflictingAddressesFailWithoutBind(t *testing.T) {
	addr, _ := withTestServerConfig(t, true)

	config.HTTPRedirectAddr = addr

	done := make(chan error, 1)

	go func() { done <- serve(context.Background(), http.NotFoundHandler(), stopPlan{timeout: time.Second}) }()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("serve не вернулся с ошибкой конфликта адресов")
	}

	assert.False(t, acceptsConn(addr), "конфликт адресов, а порт занят")
}

// TestHTTPSStopsBothListeners проверяет, что по отмене контекста освобождаются оба адреса
// и что редирект больше не закрывается через Close в defer (иначе он падал бы молча).
func TestHTTPSStopsBothListeners(t *testing.T) {
	addr, redirectAddr := withTestServerConfig(t, true)

	ctx, cancel := context.WithCancel(context.Background())

	stopped := make(chan error, 1)

	go func() { stopped <- serve(ctx, http.NotFoundHandler(), stopPlan{timeout: 10 * time.Second}) }()

	waitServing(t, addr)
	waitServing(t, redirectAddr)
	cancel()

	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve не вернулся после отмены контекста")
	}

	assert.False(t, acceptsConn(addr), "основной адрес не освобождён")
	assert.False(t, acceptsConn(redirectAddr), "адрес редиректа не освобождён")
}

// TestShutdownQuietServersReturnsNil: чистая остановка возвращает nil, приёмники закрыты в
// порядке плана, а пропущенные фазы (nil) ожиданию не мешают.
func TestShutdownQuietServersReturnsNil(t *testing.T) {
	redirect, redirectAddr := quietServer(t, "redirect")
	main, mainAddr := quietServer(t, "main")

	var mu sync.Mutex

	order := make([]string, 0, 3)

	plan := stopPlan{
		timeout: 10 * time.Second,
		closers: []io.Closer{
			&spyCloser{name: "audit-file", mu: &mu, order: &order},
			&spyCloser{name: "store", mu: &mu, order: &order},
			&spyCloser{name: "db", mu: &mu, order: &order},
		},
	}

	require.NoError(t, shutdown([]*http.Server{redirect, main}, plan))
	assert.Equal(t, []string{"audit-file", "store", "db"}, order)

	assert.False(t, acceptsConn(redirectAddr))
	assert.False(t, acceptsConn(mainAddr))
}

// TestShutdownReportsCloserError: приёмник, который не закрылся, — оборванная остановка, её
// ошибка обязана дойти до кода возврата, а остальные приёмники всё равно закрываются.
func TestShutdownReportsCloserError(t *testing.T) {
	main, _ := quietServer(t, "main")

	var mu sync.Mutex

	order := make([]string, 0, 2)

	plan := stopPlan{
		timeout: 10 * time.Second,
		closers: []io.Closer{
			&spyCloser{name: "store", mu: &mu, order: &order, err: errors.New("пул не закрылся")},
			&spyCloser{name: "db", mu: &mu, order: &order},
		},
	}

	err := shutdown([]*http.Server{main}, plan)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "пул не закрылся")
	assert.Equal(t, []string{"store", "db"}, order)
}

// TestShutdownWaitsForBackgroundAndAudit: фоновые удаления и события аудита разбираются
// раньше, чем закрыт файл аудита, иначе последние изменения не попали бы в хранилище.
func TestShutdownWaitsForBackgroundAndAudit(t *testing.T) {
	main, mainAddr := quietServer(t, "main")

	var deletes sync.WaitGroup

	deletesDone := &atomic.Bool{}
	releaseDeletes := make(chan struct{})

	deletes.Go(func() {
		<-releaseDeletes
		deletesDone.Store(true)
	})

	const events = 2

	subscriber := &gateSubscriber{release: make(chan struct{})}

	central := audit.NewCentral()
	central.Subscribe(subscriber)

	for i := range events {
		central.Notify(audit.Event{Timestamp: int64(400 + i), Action: audit.ActionShorten, URL: "https://stop.example"})
	}

	var mu sync.Mutex

	order := make([]string, 0, 3)

	plan := stopPlan{
		timeout: 10 * time.Second,
		deletes: &deletes,
		audit:   central,
		closers: []io.Closer{
			&spyCloser{name: "audit-file", mu: &mu, order: &order, atClose: func() {
				assert.True(t, deletesDone.Load(), "приёмники закрывают до завершения фонового удаления")
				assert.Equal(t, events, subscriber.count(), "файл аудита закрыли до доставки событий")
			}},
			&spyCloser{name: "store", mu: &mu, order: &order},
			&spyCloser{name: "db", mu: &mu, order: &order},
		},
	}

	stopped := make(chan error, 1)

	go func() { stopped <- shutdown([]*http.Server{main}, plan) }()

	releaseDeletes <- struct{}{}
	close(subscriber.release)

	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("остановка не дождалась фоновых задач")
	}

	assert.Equal(t, []string{"audit-file", "store", "db"}, order)
	assert.False(t, acceptsConn(mainAddr))
}

// TestShutdownDeadlineKeepsPhaseOrder проверяет ветку истёкшего периода до timeout на
// висящем обработчике: остановка ограничена одним периодом, порядок фаз сохраняется, а
// ресурсы освобождаются даже с просроченным контекстом.
func TestShutdownDeadlineKeepsPhaseOrder(t *testing.T) {
	redirect, redirectAddr := hangingServer(t)
	main, mainAddr := hangingServer(t)

	var mu sync.Mutex

	order := make([]string, 0, 3)

	plan := stopPlan{
		timeout: 500 * time.Millisecond,
		deletes: &sync.WaitGroup{},
		audit:   audit.NewCentral(),
		closers: []io.Closer{
			&spyCloser{name: "audit-file", mu: &mu, order: &order},
			&spyCloser{name: "store", mu: &mu, order: &order},
			&spyCloser{name: "db", mu: &mu, order: &order},
		},
	}

	stopped := make(chan error, 1)

	started := time.Now()

	go func() { stopped <- shutdown([]*http.Server{redirect, main}, plan) }()

	// Как только фаза редиректа началась, его слушатель отпущен, а основной ещё принимают:
	// до него очередь не дошла, потому что редирект стоит на висящем запросе.
	waitRefusing(t, redirectAddr)
	assert.True(t, acceptsConn(mainAddr), "основной слушатель закрыли раньше редиректа")

	var err error

	select {
	case err = <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("остановка не вернулась по истечении периода")
	}

	assert.Less(t, time.Since(started), 5*time.Second, "остановка вышла за пределы одного периода")
	require.ErrorIs(t, err, context.DeadlineExceeded)

	assert.False(t, acceptsConn(redirectAddr), "слушатель редиректа не отпущен")
	assert.False(t, acceptsConn(mainAddr), "основной адрес не освобождён к концу остановки")

	// Истёкший период не отменяет освобождение ресурсов.
	assert.Equal(t, []string{"audit-file", "store", "db"}, order)
}
