package grpcapi

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"study-go.ru/cho/eto/internal/authn"
)

// contextKeyUserID — ключ контекста для идентификатора пользователя,
// установленного унарным перехватчиком Auth.
type contextKeyUserID struct{}

// publicMethods — имена RPC-методов, пропускаемых без аутентификации.
// Явное перечисление не позволяет новому методу стать публичным случайно —
// каждый публичный метод добавляется осознанно.
var publicMethods = map[string]bool{
	"/shortener.v1.ShortenerService/ExpandURL": true,
}

// Auth возвращает унарный перехватчик для gRPC-сервера. Он проверяет имя вызываемого
// метода: если оно есть в publicMethods — запрос проходит без изменений. Для остальных
// извлекаются учётные данные из metadata authorization, проверяются через authn.Verify,
// а идентификатор пользователя записывается в контекст. При отказе — codes.Unauthenticated
// / codes.PermissionDenied; ошибка базы — codes.Internal (через fail).
func Auth(serviceKey string, users authn.UserStore) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		if publicMethods[info.FullMethod] {
			return handler(ctx, req)
		}

		raw, ok := credential(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, msgUnauthenticated)
		}

		usrID, err := authn.Verify(ctx, raw, serviceKey, users)
		if err != nil {
			return nil, fail("авторизация", err)
		}

		return handler(context.WithValue(ctx, contextKeyUserID{}, usrID), req)
	}
}
