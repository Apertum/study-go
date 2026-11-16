package shorten

import (
	"errors"
	"fmt"

	"github.com/lib/pq"
)

// Ошибки предметной области. Транспортам остаётся выбрать им замену: статус HTTP
// или код gRPC.
var (
	// ErrNotFound — короткая ссылка не найдена.
	ErrNotFound = errors.New("ссылка не найдена")

	// ErrGone — ссылка найдена, но помечена удалённой.
	ErrGone = errors.New("ссылка удалена")

	// ErrStorage — хранилище или база отработали с ошибкой.
	ErrStorage = errors.New("ошибка хранилища")
)

// DuplicateError — оригинальный URL уже сокращён; ShortURL содержит существующую
// короткую ссылку, чтобы транспорт мог её вернуть клиенту.
type DuplicateError struct {
	ShortURL string
}

// Error возвращает описание конфликта вместе с существующей короткой ссылкой.
func (e *DuplicateError) Error() string {
	return fmt.Sprintf("url уже сокращён: %s", e.ShortURL)
}

// isDuplicate определяет ответ базы на нарушение ограничения уникальности.
func isDuplicate(err error) bool {
	pqErr, ok := errors.AsType[*pq.Error](err)

	return ok && pqErr.Code == uniqueViolation
}
