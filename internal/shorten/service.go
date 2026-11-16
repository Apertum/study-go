package shorten

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"study-go.ru/cho/eto/internal/audit"
)

// ShortStore — хранение коротких ссылок. Ей удовлетворяет *storage.Storage и в файловом
// режиме, и в режиме PostgreSQL.
type ShortStore interface {
	// NextID возвращает следующий идентификатор записи (uuid).
	NextID() string
	// PutUnique сохраняет связку короткой и оригинальной ссылки для пользователя.
	PutUnique(uuid, shortURL, originalURL string, usrID int) error
	// GetByOriginalURL возвращает короткую ссылку, если такой оригинал уже сокращён.
	GetByOriginalURL(originalURL string) (string, bool)
	// Get возвращает оригинал по короткому id и признак удаления записи.
	Get(shortID string) (originalURL string, deleted bool, err error)
}

// UserURLReader — список ссылок пользователя.
type UserURLReader interface {
	List(ctx context.Context, usrID int) ([]Entry, error)
}

// Auditor — приёмник событий аудита. Ему удовлетворяет *audit.Central; nil означает,
// что аудит не настроен, и тогда события не ставятся.
type Auditor interface {
	Notify(audit.Event)
}

// Result — итог сокращения: идентификатор записи и обе ссылки.
type Result struct {
	UUID        string
	ShortURL    string
	OriginalURL string
}

// Entry — пара ссылок в списке пользователя. Имена полей повторяют JSON ответа
// GET /api/user/urls.
type Entry struct {
	ShortURL    string `json:"short_url"`
	OriginalURL string `json:"original_url"`
}

// Service выполняет операции предметной области. Заполняются только зависимости,
// нужные вызываемому методу: Store и BaseURL — для Shorten и Expand, URLs — для UserURLs,
// Audit необязателен всегда.
type Service struct {
	Store   ShortStore
	URLs    UserURLReader
	Audit   Auditor
	BaseURL string
}

// Shorten сокращает url от имени usrID и возвращает созданную ссылку.
//
// Пустой url здесь не отклоняется: HTTP исторически сохраняет пустой оригинал, а требование
// отклонять его относится только к gRPC- контракту, поэтому проверка осталась в адаптере.
// Если оригинал уже сокращён, возвращается *DuplicateError с существующей короткой ссылкой —
// транспорты решают сами, конфликтовать (HTTP 409, gRPC ALREADY_EXISTS) или нет.
// Прочие ошибки хранилища — ErrStorage.
func (s *Service) Shorten(usrID int, url string) (Result, error) {
	shortURL := s.BaseURL + GenerateID()
	uuid := s.Store.NextID()

	if err := s.Store.PutUnique(uuid, shortURL, url, usrID); err != nil {
		if isDuplicate(err) {
			if existing, ok := s.Store.GetByOriginalURL(url); ok {
				return Result{}, &DuplicateError{ShortURL: existing}
			}
		}

		return Result{}, fmt.Errorf("%w: сохранение ссылки: %w", ErrStorage, err)
	}

	// Событие ставится после успеха: сокращение, не попавшее в хранилище, не аудируется.
	if s.Audit != nil {
		s.Audit.Notify(audit.Event{
			Timestamp: time.Now().Unix(),
			Action:    audit.ActionShorten,
			UserID:    strconv.Itoa(usrID),
			URL:       url,
		})
	}

	return Result{UUID: uuid, ShortURL: shortURL, OriginalURL: url}, nil
}

// Expand возвращает оригинальный URL по короткому id.
//
// Нет записи (или база ничего не вернула) — ErrNotFound, удалённая запись — ErrGone.
// Успех ставит событие аудита перехода по ссылке.
func (s *Service) Expand(id string) (string, error) {
	longURL, deleted, err := s.Store.Get(id)
	if err != nil {
		return "", fmt.Errorf("%w: чтение ссылки: %w", ErrNotFound, err)
	}

	if longURL == "" {
		return "", ErrNotFound
	}

	if deleted {
		return "", ErrGone
	}

	if s.Audit != nil {
		s.Audit.Notify(audit.Event{
			Timestamp: time.Now().Unix(),
			Action:    audit.ActionFollow,
			URL:       longURL,
		})
	}

	return longURL, nil
}

// UserURLs возвращает ссылки usrID в порядке создания. Пустой список — не ошибка.
func (s *Service) UserURLs(ctx context.Context, usrID int) ([]Entry, error) {
	urls, err := s.URLs.List(ctx, usrID)
	if err != nil {
		return nil, fmt.Errorf("список ссылок пользователя %d: %w", usrID, err)
	}

	return urls, nil
}
