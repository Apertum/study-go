package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/sirupsen/logrus"
	"study-go.ru/cho/eto/internal/config"

	_ "github.com/lib/pq"
)

// Entry — одна запись URL-сокращения.
type Entry struct {
	UUID        string `json:"uuid"`
	ShortURL    string `json:"short_url"`
	OriginalURL string `json:"original_url"`
}

// Storage управляет in-memory хранилищем и синхронизацией с файлом или БД.
type Storage struct {
	mu     sync.Mutex
	store  map[string]string // shortID -> originalURL
	ids    map[string]string // uuid -> shortID
	nextID int
	path   string
	fileMu sync.Mutex

	db    *sql.DB
	useDB bool
}

// New создаёт Storage.
// Если DATABASE_DSN указан и подключение к PostgreSQL успешно,
// используются только данные из таблицы url_srv (файл не используется).
// В противном случае используется файловое хранилище.
func New(filePath string) *Storage {
	s := &Storage{
		store:  make(map[string]string),
		ids:    make(map[string]string),
		nextID: 0,
		path:   filePath,
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
	rows, err := s.db.Query("SELECT id, uuid, original_url, short_url FROM url_srv")
	if err != nil {
		logrus.WithError(err).Error("Ошибка чтения таблицы url_srv")
		return
	}
	defer rows.Close()

	s.mu.Lock()
	for rows.Next() {
		var id int
		var uuid, originalURL, shortURL string
		if err := rows.Scan(&id, &uuid, &originalURL, &shortURL); err != nil {
			logrus.WithError(err).Error("Ошибка сканирования строки url_srv")
			continue
		}
		s.store[shortURL] = originalURL
		s.ids[uuid] = shortURL
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
func (s *Storage) save() {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()

	s.mu.Lock()
	entries := make([]Entry, 0, len(s.store))
	for uuid, shortURL := range s.ids {
		entries = append(entries, Entry{
			UUID:        uuid,
			ShortURL:    shortURL,
			OriginalURL: s.store[shortURL],
		})
	}
	s.mu.Unlock()

	tmp := s.path + ".tmp"
	data, err := json.Marshal(entries)
	if err != nil {
		logrus.WithError(err).Error("Ошибка сериализации данных")
		return
	}

	if err := os.WriteFile(tmp, data, 0644); err != nil {
		logrus.WithError(err).Error("Ошибка записи temp-файла")
		return
	}

	if err := os.Rename(tmp, s.path); err != nil {
		logrus.WithError(err).Error("Ошибка переименования temp-файла")
		return
	}
}

// saveToDB сохраняет запись в таблицу url_srv.
func (s *Storage) saveToDB(uuid, shortURL, originalURL string) error {
	logrus.Debug("(uuid, original_url, short_url): " + uuid + " / " + originalURL + " // " + shortURL)
	_, err := s.db.Exec(
		"INSERT INTO url_srv (uuid, original_url, short_url) VALUES ($1, $2, $3)",
		uuid, originalURL, shortURL,
	)
	if err != nil {
		return err
	}
	return nil
}

// Put сохраняет новую запись и сразу сохраняет на диск/в БД.
func (s *Storage) Put(uuid, shortID, originalURL string) {
	s.mu.Lock()
	s.store[shortID] = originalURL
	s.ids[uuid] = shortID
	s.mu.Unlock()

	if s.useDB {
		_ = s.saveToDB(uuid, shortID, originalURL)
	} else {
		s.save()
	}
}

// PutUnique сохраняет новую запись, возвращая ошибку pq.Error при дубликате original_url.
func (s *Storage) PutUnique(uuid, shortID, originalURL string) error {
	s.mu.Lock()
	s.store[shortID] = originalURL
	s.ids[uuid] = shortID
	s.mu.Unlock()

	if s.useDB {
		return s.saveToDB(uuid, shortID, originalURL)
	}
	return nil
}

// GetByOriginalURL возвращает short_url для заданного original_url.
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

// Get возвращает оригинальный URL по короткому ID.
func (s *Storage) Get(shortID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	url, ok := s.store[shortID]
	return url, ok
}

// NextID возвращает следующий доступный номер и инкрементирует счётчик.
func (s *Storage) NextID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	return fmt.Sprintf("%d", id)
}
