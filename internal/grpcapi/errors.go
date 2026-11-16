// Package grpcapi — gRPC-оболочка над транспортно-независимым слоем сокращения ссылок.
//
// Методы делают ровно то, что делают их HTTP-аналоги, и отличаются только способом
// передать те же смыслы средствами протокола: статус HTTP становится кодом gRPC,
// тело ответа — полями сообщения, кука user_id — значением metadata authorization того же
// формата "payload:signature". Бизнес-логика не дублируется: вызывается shorten.Service,
// а учётные данные проверяет authn.Verify — та же функция, что использует AuthMiddleware.
//
// Транспорт идёт открытым текстом независимо от ENABLE_HTTPS: решение владельца зафиксировано
// в proposal этого изменения.
package grpcapi

import (
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"study-go.ru/cho/eto/internal/authn"
	"study-go.ru/cho/eto/internal/shorten"
)

// Тексты отказов статические и не различают причину: по сообщению нельзя узнать,
// отсутствовали учётные данные, были разобраны неверно или не прошли подпись.
const (
	msgUnauthenticated = "Unauthorized"
	msgForbidden       = "Forbidden"
	msgInternal        = "Internal server error"
	msgNotFound        = "Not found"
	msgGone            = "Gone"
	msgRequiredURL     = "url is required"
	msgRequiredID      = "id is required"
)

// toStatus отображает ошибку предметного слоя и проверки учётных данных в код gRPC по
// каноническому соответствию HTTP-статусов: 409 -> ALREADY_EXISTS, 410 -> FAILED_PRECONDITION,
// 401 -> UNAUTHENTICATED, 403 -> PERMISSION_DENIED.
func toStatus(err error) *status.Status {
	var dup *shorten.DuplicateError

	switch {
	case errors.Is(err, authn.ErrUnauthenticated):
		return status.New(codes.Unauthenticated, msgUnauthenticated)
	case errors.Is(err, authn.ErrUserNotFound):
		return status.New(codes.PermissionDenied, msgForbidden)
	case errors.Is(err, shorten.ErrNotFound):
		return status.New(codes.NotFound, msgNotFound)
	case errors.Is(err, shorten.ErrGone):
		return status.New(codes.FailedPrecondition, msgGone)
	case errors.As(err, &dup):
		// Существующая короткая ссылка — единственный случай, когда сообщение несёт
		// данные: контракт отвечает ALREADY_EXISTS без поля результата.
		return status.New(codes.AlreadyExists, fmt.Sprintf("url already shortened: %s", dup.ShortURL))
	default:
		return status.New(codes.Internal, msgInternal)
	}
}

// fail превращает ошибку в отказ транспорта. Нештатные ошибки (INTERNAL) пишутся в журнал:
// клиенту они ничего не говорят, а оператору без записи не разобраться. Ожидаемые отказы
// не логируются — иначе список ссылок одного пользователя превратится в поток записей.
func fail(method string, err error) error {
	st := toStatus(err)
	if st.Code() == codes.Internal {
		logrus.WithError(err).WithField("method", method).Error("gRPC: запрос не выполнен")
	}

	return st.Err()
}
