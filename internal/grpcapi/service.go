package grpcapi

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	shortenerv1 "study-go.ru/cho/eto/api/shortener/v1"
	"study-go.ru/cho/eto/internal/authn"
	"study-go.ru/cho/eto/internal/shorten"
)

// metadataAuthorization — ключ metadata с учётными данными. Формат значения совпадает со
// значением куки user_id: "payload:signature". Префикс схемы (Bearer и подобные) не
// поддерживается, metadata cookie учётные данные не несёт.
const metadataAuthorization = "authorization"

// ShortenerService выполняет методы контракта shortener.v1.ShortenerService.
type ShortenerService struct {
	shortenerv1.UnimplementedShortenerServiceServer

	service *shorten.Service
	users   authn.UserStore
	key     string
}

var _ shortenerv1.ShortenerServiceServer = (*ShortenerService)(nil)

// New собирает сервис: service — операции предметной области, users — проверка существования
// пользователя, key — тот же cookie_key, которым подписывается значение куки, выдаваемой
// POST /login.
func New(service *shorten.Service, users authn.UserStore, key string) *ShortenerService {
	return &ShortenerService{service: service, users: users, key: key}
}

// getID извлекает идентификатор пользователя из контекста, установленного перехватчиком Auth.
// Возвращает (0, false) если ключ отсутствует — значит вызов без авторизации или перехватчик не зарегистрирован.
func getID(ctx context.Context) (int, bool) {
	id, ok := ctx.Value(contextKeyUserID{}).(int)

	return id, ok
}

// ShortenURL сокращает ссылку — аналог POST /api/shorten.
//
// Результат — полный короткий URL. Повторное сокращение того же оригинала даёт ALREADY_EXISTS
// с существующей короткой ссылкой в сообщении (HTTP-аналог отвечает 409 с тем же значением).
func (s *ShortenerService) ShortenURL(ctx context.Context, req *shortenerv1.URLShortenRequest) (*shortenerv1.URLShortenResponse, error) {
	usrID, ok := getID(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, msgUnauthenticated)
	}

	// Проверка здесь, а не в shorten.Service: HTTP пустой оригинал сохраняет, и отклонять
	// его в общем слое означало бы изменить ответы HTTP.
	url := req.GetUrl()
	if url == "" {
		return nil, status.Error(codes.InvalidArgument, msgRequiredURL)
	}

	result, err := s.service.Shorten(usrID, url)
	if err != nil {
		return nil, fail("ShortenURL", err)
	}

	return &shortenerv1.URLShortenResponse{Result: result.ShortURL}, nil
}

// ExpandURL возвращает оригинал по короткому id — аналог GET /{id}. Учётные данные не
// требуются: переход по ссылке публичен и в HTTP.
func (s *ShortenerService) ExpandURL(_ context.Context, req *shortenerv1.URLExpandRequest) (*shortenerv1.URLExpandResponse, error) {
	id := req.GetId()
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, msgRequiredID)
	}

	longURL, err := s.service.Expand(id)
	if err != nil {
		return nil, fail("ExpandURL", err)
	}

	return &shortenerv1.URLExpandResponse{Result: longURL}, nil
}

// ListUserURLs выдаёт ссылки аутентифицированного пользователя — аналог GET /api/user/urls.
// Пустой список — OK без элементов: в контракте нет носителя состояния 204.
func (s *ShortenerService) ListUserURLs(ctx context.Context, _ *emptypb.Empty) (*shortenerv1.UserURLsResponse, error) {
	usrID, ok := getID(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, msgUnauthenticated)
	}

	urls, err := s.service.UserURLs(ctx, usrID)
	if err != nil {
		return nil, fail("ListUserURLs", err)
	}

	data := make([]*shortenerv1.URLData, 0, len(urls))
	for _, entry := range urls {
		data = append(data, &shortenerv1.URLData{ShortUrl: entry.ShortURL, OriginalUrl: entry.OriginalURL})
	}

	return &shortenerv1.UserURLsResponse{Url: data}, nil
}

// credential достаёт учётные данные из metadata. Их должно быть ровно одно значение:
// набор из нескольких заголовков authorization разбирается как отказ, а не как первое
// попавшееся значение.
func credential(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}

	values := md.Get(metadataAuthorization)
	if len(values) != 1 || values[0] == "" {
		return "", false
	}

	return values[0], true
}
