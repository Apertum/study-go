package grpcapi

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	shortenerv1 "study-go.ru/cho/eto/api/shortener/v1"
	"study-go.ru/cho/eto/internal/audit"
	"study-go.ru/cho/eto/internal/authn"
	"study-go.ru/cho/eto/internal/shorten"
)

const (
	testKey     = "cookie-key"
	testBase    = "http://b/"
	callTimeout = 5 * time.Second
)

// fakeStore — хранилище коротких ссылок с заданными ответами и счётчиком обращений.
type fakeStore struct {
	mu sync.Mutex

	nextID      string
	putErr      error
	duplicate   bool
	existing    string
	hasExisting bool
	originalURL string
	deleted     bool
	getErr      error
	panicValue  any

	puts int
	gets int
}

func (f *fakeStore) NextID() string {
	if f.nextID == "" {
		return "1"
	}

	return f.nextID
}

func (f *fakeStore) PutUnique(_, _, _ string, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.puts++

	if f.panicValue != nil {
		panic(f.panicValue)
	}

	switch {
	case f.putErr != nil:
		return f.putErr
	case f.duplicate:
		return &pq.Error{Code: "23505"}
	default:
		return nil
	}
}

func (f *fakeStore) GetByOriginalURL(string) (string, bool) {
	return f.existing, f.hasExisting
}

func (f *fakeStore) Get(string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.gets++

	return f.originalURL, f.deleted, f.getErr
}

func (f *fakeStore) calls() (puts, gets int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.puts, f.gets
}

// setPanic задаёт значение, которым хранилище прерывает обработку, либо снимает его.
func (f *fakeStore) setPanic(value any) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.panicValue = value
}

// fakeReader — список ссылок пользователя.
type fakeReader struct {
	urls []shorten.Entry
	err  error

	mu    sync.Mutex
	calls int
	user  int
}

func (r *fakeReader) List(_ context.Context, usrID int) ([]shorten.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls++
	r.user = usrID

	return r.urls, r.err
}

// fakeUsers — ответ проверки существования пользователя.
type fakeUsers struct {
	mu sync.Mutex

	exists bool
	err    error

	calls int
}

func (f *fakeUsers) UserExists(_ context.Context, _ int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++

	return f.exists, f.err
}

func (f *fakeUsers) invoked() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls > 0
}

// collectHandler — подписчикCentral, собирающий события аудита.
type collectHandler struct {
	mu     sync.Mutex
	events []audit.Event
}

func (h *collectHandler) Handle(event audit.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.events = append(h.events, event)
}

func (h *collectHandler) snapshot() []audit.Event {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]audit.Event(nil), h.events...)
}

// startServer поднимает адаптер на bufconn: без порта, без TLS, в пределах процесса.
func startServer(t *testing.T, svc *shorten.Service, users authn.UserStore) shortenerv1.ShortenerServiceClient {
	t.Helper()

	listener := bufconn.Listen(1024 * 64)
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(UnaryRecover()))
	shortenerv1.RegisterShortenerServiceServer(server, New(svc, users, testKey))

	go func() { _ = server.Serve(listener) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	})

	return shortenerv1.NewShortenerServiceClient(conn)
}

// callContext создаёт контекст вызова; пустое значение учётных данных означает вызов без
// metadata authorization. Metadata кладётся исходящей: клиент передаёт именно её, а серверное
// incoming-значение по каналу не уходит.
func callContext(t *testing.T, credentialValue string) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), callTimeout)
	t.Cleanup(cancel)

	if credentialValue == "" {
		return ctx
	}

	return metadata.NewOutgoingContext(ctx, metadata.New(map[string]string{metadataAuthorization: credentialValue}))
}

// testCredential собирает корректные учётные данные для payload.
func testCredential(payload string) string {
	return payload + ":" + authn.Sign(testKey, payload)
}

func TestShortenURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		store        *fakeStore
		url          string
		wantCode     codes.Code
		wantContains string
	}{
		{
			name:     "ссылка сокращена",
			store:    &fakeStore{},
			url:      "https://example.com/a",
			wantCode: codes.OK,
		},
		{
			name:         "оригинал уже сокращён",
			store:        &fakeStore{duplicate: true, existing: testBase + "old1", hasExisting: true},
			url:          "https://example.com/dup",
			wantCode:     codes.AlreadyExists,
			wantContains: testBase + "old1",
		},
		{
			name:     "ошибка хранилища",
			store:    &fakeStore{putErr: errors.New("база недоступна")},
			url:      "https://example.com/b",
			wantCode: codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := startServer(t, &shorten.Service{Store: tt.store, BaseURL: testBase}, &fakeUsers{exists: true})

			resp, err := client.ShortenURL(callContext(t, testCredential("42")), &shortenerv1.URLShortenRequest{Url: tt.url})

			if tt.wantCode != codes.OK {
				require.Equal(t, tt.wantCode, status.Code(err))
				if tt.wantContains != "" {
					assert.Contains(t, err.Error(), tt.wantContains)
				}

				return
			}

			require.NoError(t, err)
			assert.True(t, strings.HasPrefix(resp.GetResult(), testBase), "короткий URL собран из BaseURL: %s", resp.GetResult())
			assert.Len(t, resp.GetResult(), len(testBase)+8)
		})
	}
}

func TestShortenURL_EmptyURL(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	client := startServer(t, &shorten.Service{Store: store, BaseURL: testBase}, &fakeUsers{exists: true})

	_, err := client.ShortenURL(callContext(t, testCredential("42")), &shortenerv1.URLShortenRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	puts, _ := store.calls()
	assert.Zero(t, puts, "пустой url отклоняется до обращения к хранилищу")
}

func TestExpandURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		store    *fakeStore
		id       string
		wantCode codes.Code
		wantURL  string
	}{
		{
			name:     "ссылка найдена",
			store:    &fakeStore{originalURL: "https://example.com/a"},
			id:       "a1b2c3d4",
			wantCode: codes.OK,
			wantURL:  "https://example.com/a",
		},
		{name: "ссылки нет", store: &fakeStore{}, id: "ffffffff", wantCode: codes.NotFound},
		{name: "ссылка удалена", store: &fakeStore{originalURL: "https://example.com/a", deleted: true}, id: "a1b2c3d4", wantCode: codes.FailedPrecondition},
		{name: "ошибка хранилища", store: &fakeStore{getErr: errors.New("оборвалось")}, id: "a1b2c3d4", wantCode: codes.NotFound},
		{name: "пустой id", store: &fakeStore{}, id: "", wantCode: codes.InvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := startServer(t, &shorten.Service{Store: tt.store, BaseURL: testBase}, &fakeUsers{})

			// Переход по ссылке публичен: вызов идёт без учётных данных.
			resp, err := client.ExpandURL(callContext(t, ""), &shortenerv1.URLExpandRequest{Id: tt.id})

			if tt.wantCode != codes.OK {
				require.Equal(t, tt.wantCode, status.Code(err))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, resp.GetResult())
		})
	}
}

func TestListUserURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		reader   *fakeReader
		wantCode codes.Code
		wantLen  int
	}{
		{
			name:     "список отдан",
			reader:   &fakeReader{urls: []shorten.Entry{{ShortURL: testBase + "a1", OriginalURL: "https://one"}, {ShortURL: testBase + "b2", OriginalURL: "https://two"}}},
			wantCode: codes.OK,
			wantLen:  2,
		},
		{name: "пустой список", reader: &fakeReader{}, wantCode: codes.OK},
		{name: "ошибка чтения", reader: &fakeReader{err: errors.New("база молчит")}, wantCode: codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := startServer(t, &shorten.Service{URLs: tt.reader}, &fakeUsers{exists: true})

			resp, err := client.ListUserURLs(callContext(t, testCredential("42")), &emptypb.Empty{})
			require.Equal(t, tt.wantCode, status.Code(err))

			if tt.wantCode != codes.OK {
				return
			}

			require.Len(t, resp.GetUrl(), tt.wantLen)

			if tt.wantLen == 0 {
				return
			}

			assert.Equal(t, testBase+"a1", resp.GetUrl()[0].GetShortUrl())
			assert.Equal(t, "https://one", resp.GetUrl()[0].GetOriginalUrl())
			assert.Equal(t, 42, tt.reader.user)
		})
	}
}

// TestUnauthorizedRefusalsAreIndistinguishable проверяет, что отказ по учётным данным не
// различается по причине: код и сообщение одинаковы для отсутствия, неверной формы, неверной
// подписи и учётных данных, переданных в чужом ключе metadata.
func TestUnauthorizedRefusalsAreIndistinguishable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ctx  func(t *testing.T) context.Context
	}{
		{name: "без metadata", ctx: func(t *testing.T) context.Context { return callContext(t, "") }},
		{name: "значение без разделителя", ctx: func(t *testing.T) context.Context { return callContext(t, "42") }},
		{name: "неверная подпись", ctx: func(t *testing.T) context.Context { return callContext(t, "42:deadbeef") }},
		{name: "пустое значение", ctx: func(t *testing.T) context.Context { return callContext(t, ":") }},
		{
			name: "учётные данные в metadata cookie",
			ctx: func(t *testing.T) context.Context {
				return metadata.NewOutgoingContext(callContext(t, ""),
					metadata.New(map[string]string{"cookie": "user_id=" + testCredential("42")}))
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			users := &fakeUsers{exists: true}
			client := startServer(t, &shorten.Service{Store: &fakeStore{}, BaseURL: testBase}, users)

			_, err := client.ListUserURLs(tt.ctx(t), &emptypb.Empty{})

			st, ok := status.FromError(err)
			require.True(t, ok, "ожидался отказ gRPC, получено: %v", err)
			assert.Equal(t, codes.Unauthenticated, st.Code())
			assert.Equal(t, msgUnauthenticated, st.Message())
			assert.False(t, users.invoked())
		})
	}
}

func TestAuthorization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cred     func() string
		users    *fakeUsers
		wantCode codes.Code
	}{
		{
			name:     "корректные учётные данные",
			cred:     func() string { return testCredential("42") },
			users:    &fakeUsers{exists: true},
			wantCode: codes.OK,
		},
		{
			name:     "пользователя нет",
			cred:     func() string { return testCredential("42") },
			users:    &fakeUsers{},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "ошибка базы при проверке пользователя",
			cred:     func() string { return testCredential("42") },
			users:    &fakeUsers{err: errors.New("база недоступна")},
			wantCode: codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := &fakeReader{}
			client := startServer(t, &shorten.Service{URLs: reader}, tt.users)

			_, err := client.ListUserURLs(callContext(t, tt.cred()), &emptypb.Empty{})
			require.Equal(t, tt.wantCode, status.Code(err))

			if tt.wantCode == codes.OK {
				assert.Equal(t, 42, reader.user, "идентификатор из учётных данных дошёл до слоя ссылок")
			}
		})
	}
}

func TestAuthorizationWithoutCredentialDoesNotTouchStorage(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	users := &fakeUsers{exists: true}
	client := startServer(t, &shorten.Service{Store: store, BaseURL: testBase}, users)

	_, err := client.ShortenURL(callContext(t, ""), &shortenerv1.URLShortenRequest{Url: "https://example.com/a"})
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	puts, _ := store.calls()
	assert.Zero(t, puts)
	assert.False(t, users.invoked(), "отказ по отсутствию учётных данных не идёт в базу за пользователем")
}

func TestSeveralAuthorizationValuesRejected(t *testing.T) {
	t.Parallel()

	client := startServer(t, &shorten.Service{URLs: &fakeReader{}}, &fakeUsers{exists: true})

	ctx := metadata.NewOutgoingContext(callContext(t, ""), metadata.Pairs(
		metadataAuthorization, testCredential("42"),
		metadataAuthorization, testCredential("7"),
	))

	_, err := client.ListUserURLs(ctx, &emptypb.Empty{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// TestAuditEventsMatchHTTP сверяет события аудита gRPC-вызовов с HTTP-аналогами: тот же тип
// действия, тот же пользователь и тот же оригинальный URL.
func TestAuditEventsMatchHTTP(t *testing.T) {
	t.Parallel()

	store := &fakeStore{originalURL: "https://example.com/a"}
	handler := &collectHandler{}
	central := audit.NewCentral()
	central.Subscribe(handler)

	svc := &shorten.Service{Store: store, Audit: central, BaseURL: testBase}
	client := startServer(t, svc, &fakeUsers{exists: true})

	ctx := callContext(t, testCredential("42"))

	shortened, err := client.ShortenURL(ctx, &shortenerv1.URLShortenRequest{Url: "https://example.com/a"})
	require.NoError(t, err)

	_, err = client.ExpandURL(ctx, &shortenerv1.URLExpandRequest{Id: shortened.GetResult()})
	require.NoError(t, err)

	require.NoError(t, central.Wait(ctx))

	events := handler.snapshot()
	require.Len(t, events, 2)

	assert.Equal(t, audit.ActionShorten, events[0].Action)
	assert.Equal(t, "42", events[0].UserID)
	assert.Equal(t, "https://example.com/a", events[0].URL)

	assert.Equal(t, audit.ActionFollow, events[1].Action)
	assert.Empty(t, events[1].UserID)
	assert.Equal(t, "https://example.com/a", events[1].URL)
}
