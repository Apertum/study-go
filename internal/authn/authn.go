// Package authn проверяет учётные данные, которые сервис выдаёт ответом POST /login.
//
// Учётные данные — строка "payload:signature", где payload — числовой идентификатор
// пользователя, а signature — HMAC-SHA256 от payload по ключу COOKIE_KEY в hex-представлении.
// Один разбор используют оба транспорта: HTTP-middleware (internal/handler.AuthMiddleware,
// значение куки user_id) и gRPC-адаптер (internal/grpcapi, metadata authorization), поэтому
// требования к подписи не могут разойтись между протоколами.
//
// Разбор идёт в порядке проверки: сначала подпись, только потом содержательная часть. Иначе
// отказ по форме выдавал бы информацию о том, какая именно часть учётных данных неверна.
package authn

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// signatureSeparator разделяет payload и подпись в значении учётных данных.
const signatureSeparator = ":"

// credentialParts — сколько частей даёт корректное значение после разбиения по separator:
// ровно две. Три части означают лишнее двоеточие в payload либо подписи.
const credentialParts = 2

var (
	// ErrUnauthenticated ставится, когда учётные данные отсутствуют, неразбираемы или
	// подпись не совпала. Причина намеренно не различается: текст отказа ничего не
	// сообщает о том, какой именно проверки не прошло.
	ErrUnauthenticated = errors.New("учётные данные не приняты")

	// ErrUserNotFound ставится, когда подпись корректна, но пользователя нет в базе
	// (HTTP 403, gRPC PERMISSION_DENIED).
	ErrUserNotFound = errors.New("пользователь не найден")

	// ErrInternal оборачивает ошибку хранилища пользователей: отказ не должен выглядеть
	// как «нет доступа».
	ErrInternal = errors.New("внутренняя ошибка")
)

// UserStore отвечает на вопрос, есть ли пользователь с таким идентификатором.
type UserStore interface {
	UserExists(ctx context.Context, usrID int) (bool, error)
}

// Sign возвращает HMAC-SHA256 подпись payload по ключу key в hex-представлении.
func Sign(key, payload string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(payload))

	return hex.EncodeToString(mac.Sum(nil))
}

// Verify разбирает и проверяет учётные данные raw, возвращая идентификатор пользователя.
//
// Отказ возвращает как ErrUnauthenticated (пустое значение, нет ровно одного разделителя,
// подпись не совпала), как ErrUserNotFound (подпись верна, но payload не число или
// пользователя нет в базе) либо как ErrInternal, обернув ошибку базы.
func Verify(ctx context.Context, raw, key string, users UserStore) (int, error) {
	payload, signature, ok := split(raw)
	if !ok {
		return 0, ErrUnauthenticated
	}

	if !hmac.Equal([]byte(signature), []byte(Sign(key, payload))) {
		return 0, ErrUnauthenticated
	}

	usrID, err := strconv.Atoi(payload)
	if err != nil {
		return 0, ErrUserNotFound
	}

	exists, err := users.UserExists(ctx, usrID)
	if err != nil {
		return 0, fmt.Errorf("%w: проверка пользователя: %w", ErrInternal, err)
	}

	if !exists {
		return 0, ErrUserNotFound
	}

	return usrID, nil
}

// split разбивает значение учётных данных на payload и подпись. Разделитель обязан
// встречаться ровно один раз.
func split(raw string) (payload, signature string, ok bool) {
	parts := strings.SplitN(raw, signatureSeparator, credentialParts+1)
	if len(parts) != credentialParts {
		return "", "", false
	}

	return parts[0], parts[1], true
}
