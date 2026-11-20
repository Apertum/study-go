// Package shorten содержит правила сокращения ссылок, их расширения и выдачи
// списка ссылок пользователя.
//
// Это транспортно-независимый слой: HTTP-хендлеры (internal/handler) и gRPC-адаптер
// (internal/grpcapi) вызывает его методы и самостоятельно отображает ошибки в статус
// HTTP либо в код gRPC. Поэтому здесь нет ни http, ни grpc: только предметная область
// и различимые ошибки (ErrNotFound, ErrGone, ErrStorage, *DuplicateError).
//
// Зависимости описаны минимальными интерфейсами (ShortStore, UserURLReader, Auditor),
// что позволяет проверять слой тестами без PostgreSQL и без сетевых портов.
package shorten

import (
	"crypto/rand"
	"encoding/hex"
)

// uniqueViolation — код SQLSTATE уникальности PostgreSQL: им база отвечает на
// повторное сокращение уже существующего оригинального URL.
const uniqueViolation = "23505"

// idBytes — длина случайного идентификатора короткой ссылки в байтах;
// в hex-представлении это вдвое больше символов.
const idBytes = 4

// GenerateID возвращает случайный идентификатор короткой ссылки.
func GenerateID() string {
	b := make([]byte, idBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
