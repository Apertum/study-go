package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// Action — тип действия аудита.
type Action string

const (
	ActionShorten Action = "shorten"
	ActionFollow  Action = "follow"
)

// Event — событие аудита.
type Event struct {
	Timestamp int64  `json:"ts"`
	Action    Action `json:"action"`
	UserID    string `json:"user_id"`
	URL       string `json:"url"`
}

// MarshalJSON кастомная сериализация для исключения пустого user_id из JSON.
// Когда user_id не задан, поле полностью исключается из выходного документа.
func (e Event) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(`{"ts":`)
	buf.WriteString(fmt.Sprintf("%d", e.Timestamp))
	buf.WriteString(`,"action":"`)
	buf.WriteString(string(e.Action))
	buf.WriteString(`","url":"`)
	buf.WriteString(e.URL)
	buf.WriteString(`"}`)
	if e.UserID != "" {
		// вставляем user_id перед закрывающей скобкой
		data := buf.Bytes()
		result := make([]byte, 0, len(data)+len(e.UserID)+12)
		result = append(result, data[:len(data)-1]...)
		result = append(result, `,"user_id":"`...)
		result = append(result, []byte(e.UserID)...)
		result = append(result, '"', '}')
		return result, nil
	}
	return buf.Bytes(), nil
}

// EventHandler — интерфейс наблюдателя.
type EventHandler interface {
	Handle(event Event)
}

// FileHandler — наблюдатель: запись в файл (append, одна строка).
type FileHandler struct {
	mu   sync.Mutex
	file *os.File
}

// NewFileHandler создаёт файл-приёмник в режиме дозаписи.
// Файл открывается один раз при старте и держится открытым до shutdown сервера.
// Каждое событие дописывается в конец файла одной JSON-строкой (append mode).
// Мьютекс гарантирует, что одновременные уведомления от разных горутин
// не перемешают записи на диске — каждая запись происходит целиком атомарно.
func NewFileHandler(path string) (*FileHandler, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("audit file handler: %w", err)
	}
	return &FileHandler{file: f}, nil
}

// Handle записывает одно событие как JSON-строку на новую строку.
func (h *FileHandler) Handle(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	_, _ = h.file.Write(data)
	_, _ = h.file.Write([]byte("\n"))
}

// Close закрывает файл.
func (h *FileHandler) Close() error {
	if h.file != nil {
		return h.file.Close()
	}
	return nil
}

// URLHandler — наблюдатель: отправка POST на удалённый сервер.
type URLHandler struct {
	client *http.Client
	url    string
}

// NewURLHandler создаёт обработчик с указанным URL и таймаутом 5 секунд.
func NewURLHandler(url string) *URLHandler {
	return &URLHandler{
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		url: url,
	}
}

// Handle отправляет событие POST-запросом на удалённый сервер.
func (h *URLHandler) Handle(event Event) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	resp, err := h.client.Post(h.url, "application/json", bytes.NewReader(data))
	if err != nil {
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
}

// Central — центральный аудит-реестр (паттерн Наблюдатель).
type Central struct {
	handlers []EventHandler
	mu       sync.RWMutex
}

// NewCentral создаёт центральный аудит-реестр.
func NewCentral() *Central {
	return &Central{
		handlers: make([]EventHandler, 0),
	}
}

// Subscribe добавляет наблюдателя.
func (c *Central) Subscribe(h EventHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers = append(c.handlers, h)
}

// Notify уведомляет всех наблюдателей.
func (c *Central) Notify(event Event) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, h := range c.handlers {
		go h.Handle(event)
	}
}
