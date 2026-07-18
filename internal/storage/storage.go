package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/sirupsen/logrus"
)

// Entry — одна запись URL-сокращения.
type Entry struct {
	UUID       string `json:"uuid"`
	ShortURL   string `json:"short_url"`
	OriginalURL string `json:"original_url"`
}

// Storage управляет in-memory хранилищем и синхронизацией с файлом.
type Storage struct {
	mu     sync.Mutex
	store  map[string]string // shortID -> originalURL
	ids    map[string]string // uuid -> shortID
	nextID int
	path   string
	fileMu sync.Mutex
}

// New создаёт Storage и загружает данные из файла (если он существует). Метод к структуре типа ООП.
func New(filePath string) *Storage {
	s := &Storage{
		store:  make(map[string]string),
		ids:    make(map[string]string),
		nextID: 0,
		path:   filePath,
	}
	s.load()
	return s
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
		id, _ := parseInt(e.UUID)
		if id >= s.nextID {
			s.nextID = id + 1
		}
	}
	s.mu.Unlock()

	logrus.Infof("Загружено %d записей из %s", len(entries), s.path)
}

// save записывает все данные в файл (атомарно через temp-file + rename).
func (s *Storage) save() {
	s.fileMu.Lock() // это мне подсказали
	defer s.fileMu.Unlock()

	s.mu.Lock()
	entries := make([]Entry, 0, len(s.store))
	for uuid, shortURL := range s.ids {
		entries = append(entries, Entry{
			UUID:       uuid,
			ShortURL:   shortURL,
			OriginalURL: s.store[shortURL],
		})
	}
	s.mu.Unlock()

	tmp := s.path + ".tmp"
	data, err := json.Marshal(entries) // странно сортирует. надо бы сортирнуть по id руками
	if err != nil { // if-программирование, бесит
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

// Put сохраняет новую запись и сразу сохраняет на диск.
func (s *Storage) Put(uuid, shortID, originalURL string) {
	s.mu.Lock()
	s.store[shortID] = originalURL
	s.ids[uuid] = shortID
	s.mu.Unlock()

	s.save()
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

// parseInt парсит int из строки.
func parseInt(s string) (int, error) {
	result := 0
	negative := false
	for _, c := range s {
		if c == '-' {
			negative = true
			continue
		}
		if c < '0' || c > '9' {
			return 0, os.ErrInvalid
		}
		result = result*10 + int(c-'0')
	}
	if negative {
		return -result, nil
	}
	return result, nil
}
