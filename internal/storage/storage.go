// Package storage предоставляет слой хранения для URL-сократителя.
//
// Storage поддерживает два бэкенда:
//   - PostgreSQL — при указании DATABASE_DSN и успешном подключении
//   - JSON-файл — fallback при отсутствии БД
//
// Данные загружаются в in-memory map при старте и синхронизируются с выбраным бэкендом.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/lib/pq"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
	"study-go.ru/cho/eto/internal/config"
)

// Entry — одна запись сокращённой ссылки в хранилище.
//
// Поля:
//   - UUID — уникальный идентификатор записи (числовой, в строковом представлении)
//   - ShortURL — короткая ссылка (например, "http://short.ru/a1b2c3d4")
//   - OriginalURL — оригинальная (длинная) ссылка
//   - UserID — идентификатор пользователя, создавшего ссылку
type Entry struct {
	UUID        string `json:"uuid"`
	ShortURL    string `json:"short_url"`
	OriginalURL string `json:"original_url"`
	UserID      int    `json:"user_id,omitempty"`
	DeletedFlag bool   `db:"is_deleted"`
}

// Storage управляет in-memory хранилищем и синхронизацией с JSON-файлом или PostgreSQL.
//
// Поддерживает два режима работы:
//   - PostgreSQL — при заданном DATABASE_DSN и успешном подключении; все операции идут в БД.
//   - Файловый — fallback; данные хранятся в JSON-файле, чтение/запись атомарные через temp-file+rename.
//
// При создании (New) данные загружаются из выбранного бэкенда в in-memory мапы.
type Storage struct {
	mu       sync.Mutex
	store    map[string]string // shortURL -> originalURL
	ids      map[string]string // uuid -> shortURL
	urlUsers map[string]int    // shortURL -> usrID
	nextID   int
	path     string
	fileMu   sync.Mutex

	db    *sql.DB
	useDB bool
}

// New создаёт Storage с выбранным бэкендом хранения.
//
// Логика выбора бэкенда:
//  1. Если config.DatabaseDSN не пустой и подключение к PostgreSQL успешно — используется БД.
//     Все данные загружаются из таблицы url_srv в in-memory мапы.
//  2. Иначе — используется файловое хранилище; данные загружаются из filePath.
//
// Параметры:
//   - filePath — путь к JSON-файлу (используется только при файловом режиме).
//
// Возвращает готовый к использованию *Storage.
func New(filePath string) *Storage {
	s := &Storage{
		store:    make(map[string]string),
		ids:      make(map[string]string),
		urlUsers: make(map[string]int),
		nextID:   0,
		path:     filePath,
	}

	if config.DatabaseDSN != "" {
		db, err := sql.Open("postgres", config.DatabaseDSN)
		if err != nil {
			logrus.WithError(err).Warn("Не удалось открыть подключение к PostgreSQL, используется файловое хранилище")
		} else if err := db.Ping(); err != nil {
			db.Close()
			logrus.WithError(err).Warn("Не удалось подключиться к PostgreSQL, используется файловое хранилище")
		} else {
			logrus.Info("Подключение к PostgreSQL успешно, используется база данных")
			s.db = db
			s.useDB = true
			s.loadFromDB()
			return s
		}
	}

	s.load()
	return s
}

// loadFromDB читает все записи из url_srv и восстанавливает in-memory состояние.
func (s *Storage) loadFromDB() {
	rows, err := s.db.Query("SELECT id, uuid, original_url, short_url, COALESCE(usr_id, 0) FROM url_srv")
	if err != nil {
		logrus.WithError(err).Error("Ошибка чтения таблицы url_srv")
		return
	}
	defer rows.Close()

	s.mu.Lock()
	for rows.Next() {
		var id int
		var uuid, originalURL, shortURL string
		var usrID int
		if err := rows.Scan(&id, &uuid, &originalURL, &shortURL, &usrID); err != nil {
			logrus.WithError(err).Error("Ошибка сканирования строки url_srv")
			continue
		}
		s.store[shortURL] = originalURL
		s.ids[uuid] = shortURL
		s.urlUsers[shortURL] = usrID
		if id >= s.nextID {
			s.nextID = id + 1
		}
	}
	s.mu.Unlock()

	if err := rows.Err(); err != nil {
		logrus.WithError(err).Error("Ошибка при итерации по строкам url_srv")
	}

	logrus.Info("Загружено записей из БД: ", len(s.ids))
}

// load читает файл и восстанавливает in-memory состояние.
func (s *Storage) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			logrus.Info("Файл хранения не существует, начнём с пустого хранилища")
		} else {
			logrus.WithError(err).Error("Ошибка чтения файла хранения")
		}
		return
	}

	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		logrus.WithError(err).Error("Ошибка парсинга файла хранения")
		return
	}

	s.mu.Lock()
	for _, e := range entries {
		s.store[e.ShortURL] = e.OriginalURL
		s.ids[e.UUID] = e.ShortURL
		s.urlUsers[e.ShortURL] = e.UserID
		id, err := strconv.Atoi(e.UUID)
		if err != nil {
			fmt.Println("Штош. Ошибка при парсинге:", err)
			return
		}
		if id >= s.nextID {
			s.nextID = id + 1
		}
	}
	s.mu.Unlock()

	logrus.Infof("Загружено %d записей из %s", len(entries), s.path)
}

// save записывает все данные в файл (атомарно через temp-file + rename).
func (s *Storage) save(uuid, shortURL, originalURL string, usrID int) error {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()

	for _, val := range s.store {
		if val == originalURL {
			logrus.Error("DOUBLE: " + originalURL)
			// небольшой хак. Возвращаем указатель на структуру pq.Error с нужным кодом
			return &pq.Error{
				Code:    "23505",
				Message: "unique_violation: url already exists",
			}
		}
	}

	s.mu.Lock()
	s.store[shortURL] = originalURL
	s.ids[uuid] = shortURL
	s.urlUsers[shortURL] = usrID
	entries := make([]Entry, 0, len(s.store))
	for uuid, shortURL := range s.ids {
		entries = append(entries, Entry{
			UUID:        uuid,
			ShortURL:    shortURL,
			OriginalURL: s.store[shortURL],
			UserID:      s.urlUsers[shortURL],
		})
	}
	s.mu.Unlock()

	tmp := s.path + ".tmp"
	data, err := json.Marshal(entries)
	if err != nil {
		logrus.WithError(err).Error("Ошибка сериализации данных")
		return err
	}

	if err := os.WriteFile(tmp, data, 0644); err != nil {
		logrus.WithError(err).Error("Ошибка записи temp-файла")
		return err
	}

	if err := os.Rename(tmp, s.path); err != nil {
		logrus.WithError(err).Error("Ошибка переименования temp-файла")
		return err
	}
	return nil
}

// saveToDB сохраняет запись в таблицу url_srv.
func (s *Storage) saveToDB(uuid, shortURL, originalURL string, usrID int) error {
	logrus.Debug("(uuid, original_url, short_url, usr_id): " + uuid + " / " + originalURL + " // " + shortURL + " / " + fmt.Sprintf("%d", usrID))
	_, err := s.db.Exec(
		"INSERT INTO url_srv (uuid, original_url, short_url, usr_id) VALUES ($1, $2, $3, $4)",
		uuid, originalURL, shortURL, usrID,
	)
	if err != nil {
		return err
	}
	return nil
}

// PutUnique сохраняет новую запись URL-сокращения.
//
// Возвращает pq.Error с кодом "23505" (unique_violation), если original_url уже существует.
// В файловом режиме ошибка возвращается через save(); в БД — через INSERT конфликт уникальности.
func (s *Storage) PutUnique(uuid, shortURL, originalURL string, usrID int) error {
	if s.useDB {
		return s.saveToDB(uuid, shortURL, originalURL, usrID)
	} else {
		return s.save(uuid, shortURL, originalURL, usrID)
	}
}

// GetByOriginalURL возвращает short_url для заданного original_url из in-memory мапы.
//
// Возвращает (shortURL, true) при нахождении или ("", false) если URL не найден.
func (s *Storage) GetByOriginalURL(originalURL string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for shortURL, url := range s.store {
		if url == originalURL {
			return shortURL, true
		}
	}
	return "", false
}

// Get возвращает оригинальный URL по короткому ID и флаг удаления.
//
// В PostgreSQL режиме выполняется запрос к таблице url_srv.
// В файловом режиме механизм удаления отсутствует, deleted всегда false.
//
// Возвращает:
//   - originalURL — найденный оригинальный URL
//   - deleted — true если запись помечена как удалённая (только для БД)
//   - err — ошибка запроса, если возникла
func (s *Storage) Get(shortID string) (originalURL string, deleted bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.useDB {
		return selectUrl(s, shortID)
	} else {
		url, _ := s.store[shortID]
		return url, false, nil
	}
}

func selectUrl(s *Storage, id string) (string, bool, error) {
	type urlEntry struct {
		ShortURL    string `json:"short_url"`
		OriginalURL string `json:"original_url"`
		IsDeleted   bool   `json:"is_deleted"`
	}

	rows, err := s.db.Query(
		"SELECT short_url, original_url, deleted as is_deleted FROM url_srv WHERE short_url like $1 ",
		fmt.Sprintf("%%%s", id),
	)
	if err != nil {
		logrus.WithError(err).Error("Failed to query user URLs")
		return "", false, err
	}
	defer rows.Close()

	var urls []urlEntry
	for rows.Next() {
		var entry urlEntry
		if err := rows.Scan(&entry.ShortURL, &entry.OriginalURL, &entry.IsDeleted); err != nil {
			logrus.WithError(err).Error("Failed to scan URL row")
			return "", false, err
		}
		urls = append(urls, entry)
	}
	if err := rows.Err(); err != nil {
		logrus.WithError(err).Error("Error iterating URL rows")
		return "", false, err
	}

	if urls == nil || len(urls) == 0 {
		return "", false, nil
	} else {
		return urls[0].OriginalURL, urls[0].IsDeleted, nil
	}
}

// NextID возвращает следующий доступный номер-идентификатор и инкрементирует внутренний счётчик.
//
// Используется как UUID для новых записей. Безопасен для конкурентного доступа благодаря мьютексу.
// Возвращает строковое представление числа (например, "0", "1", "2").
func (s *Storage) NextID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	return fmt.Sprintf("%d", id)
}

// DeleteUrls выполняет мягкое удаление (soft delete) записей для заданного пользователя.
//
// Помечает строки в таблице url_srv, где usr_id совпадает и short_url совпадает
// с любым из переданных URL (через PostgreSQL regex). Возвращает ошибку выполнения запроса.
// Используется хэндлером DeleteURLs; вызывается в отдельной горутине.
func (s *Storage) DeleteUrls(ctx context.Context, usrID int, forDel []string) error {
	// Объединяем через разделитель "|"
	suffix := strings.Join(forDel, "|")

	rows, err := s.db.QueryContext(ctx,
		"update url_srv set deleted=true WHERE usr_id = $1 and short_url ~ $2",
		usrID,
		fmt.Sprintf("(%s)$", suffix), // Формируем регулярное выражение на стороне Go
	)
	defer rows.Close()
	if err != nil {
		logrus.WithError(err).Errorf("Failed to DELETE user URLs: %s", suffix)
	} else {
		logrus.Infof("Удалили: %s", suffix)
	}
	return err
}
