// Package audit реализует паттерн «Наблюдатель» для аудита HTTP-запросов URL-сократителя.
//
// После успешной обработки хендлерами POST /, POST /api/shorten или GET /{id}
// формируется событие и отправляется всем подключённым приёмникам:
//   - FileHandler — дописывает JSON-строку в файл (append mode)
//   - URLHandler — отправляет событие POST-запросом на удалённый сервер
//
// Все уведомления отправляются в отдельных горутинах, чтобы не блокировать хендлер.
//
// Пример:
//
//	central := audit.NewCentral()
//
//	fileH, err := audit.NewFileHandler("/var/log/audit.jsonl")
//	if err != nil { ... }
//	central.Subscribe(fileH)
//	defer fileH.Close()
//
//	urlH := audit.NewURLHandler("https://monitor.internal/api/audit")
//	central.Subscribe(urlH)
//
//	// В хендлере после успешной обработки:
//	central.Notify(audit.Event{
//	    Timestamp: time.Now().Unix(),
//	    Action:    audit.ActionShorten,
//	    UserID:    userIDStr,
//	    URL:       longURL,
//	})
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
//
// Константы:
//   - ActionShorten — создание сокращённой ссылки (POST /, POST /api/shorten)
//   - ActionFollow  — переход по короткой ссылке (GET /{id})
type Action string

const (
	// ActionShorten — действие «сокращение ссылки». Фиксируется при создании
	// новой короткой ссылки через POST / или POST /api/shorten.
	ActionShorten Action = "shorten"

	// ActionFollow — действие «переход по ссылке». Фиксируется при обращении
	// к короткой ссылке через GET /{id}.
	ActionFollow Action = "follow"
)

// Event — событие аудита, передаваемое наблюдателям через Central.Notify.
// Поле UserID исключается из JSON при пустом значении (через кастомный MarshalJSON).
type Event struct {
	// Timestamp — unix-временя события в секундах.
	Timestamp int64 `json:"ts"`
	// Action — тип действия: "shorten" или "follow".
	Action Action `json:"action"`
	// UserID — идентификатор пользователя. Пустое поле исключается из JSON-вывода.
	UserID string `json:"user_id"`
	// URL — оригинальный (не сокращённый) URL, над которым выполнено действие.
	URL string `json:"url"`
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

// EventHandler — интерфейс для приёмников аудита. Каждый наблюдатель реализует
// метод Handle(Event), который вызывается Central.Notify при каждом событии.
// Реализации (FileHandler, URLHandler) запускаются в отдельных горутинах,
// поэтому метод не должен блокировать выполнение хэндлера.
type EventHandler interface {
	Handle(event Event)
}

// FileHandler — наблюдатель, записывающий события аудита в файл.
// Файл открывается один раз при создании и остаётся открытым до вызова Close().
// Каждая запись происходит атомарно благодаря мьютексу.
type FileHandler struct {
	mu   sync.Mutex
	file *os.File
}

// NewFileHandler создаёт FileHandler для указаннного пути файла.
// Открывает файл в режиме дозаписи (append), создаёт если не существует.
// Возвращает ошибку, если директория не существует или файл недоступен для записи.
//
// Пример:
//
//	handler, err := audit.NewFileHandler("/var/log/shortener_audit.jsonl")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer handler.Close()
func NewFileHandler(path string) (*FileHandler, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("audit file handler: %w", err)
	}
	return &FileHandler{file: f}, nil
}

// Handle записывает событие как одну JSON-строку на новой строке в открытый файл.
// Вызывается из Central.Notify в отдельной горутине.
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

// Close закрывает файл и освобождает ресурсы. Безопасен для повторного вызова —
// после первого Close ничего не произойдёт.
func (h *FileHandler) Close() error {
	if h.file != nil {
		return h.file.Close()
	}
	return nil
}

// URLHandler — наблюдатель, отправляющий события аудита POST-запросом на удалённый сервер.
// Использует http.Client с таймаутом 5 секунд; ошибки отправки молча игнорируются,
// чтобы не блокировать хэндлер.
type URLHandler struct {
	client *http.Client
	url    string
}

// NewURLHandler создаёт URLHandler для указанного URL-адреса приёмника.
// Настроенный http.Client имеет таймаут 5 секунд на весь запрос.
//
// Пример:
//
//	handler := audit.NewURLHandler("https://monitor.internal/api/audit")
func NewURLHandler(url string) *URLHandler {
	return &URLHandler{
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		url: url,
	}
}

// Handle отправляет событие JSON-телом в POST-запросе на указанный URL.
// Ошибки сети или HTTP-ответы игнорируются — метод не должен блокировать callers.
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

// Central — центральный реестр наблюдателей (паттерн «Наблюдатель»).
// Хранит список обработчиков, которые уведомляются при каждом событии аудита.
// Все уведомления отправляются в отдельных горутинах, чтобы не блокировать основной поток хэндлера.
//
// Подход «publish-subscribe»: обработчики подписываются через Subscribe(),
// а хэндлеры вызывают Notify() после успешной обработки запроса.
type Central struct {
	handlers []EventHandler
	mu       sync.RWMutex
}

// NewCentral создаёт новый пустой Central для подписки наблюдателей.
// Возвращённый объект готов к использованию — Subscribe и Notify можно вызывать сразу.
//
// Пример:
//
//	central := audit.NewCentral()
//	central.Subscribe(audit.NewFileHandler("audit.log"))
//	central.Subscribe(audit.NewURLHandler("https://monitor.internal/audit"))
func NewCentral() *Central {
	return &Central{
		handlers: make([]EventHandler, 0),
	}
}

// Subscribe добавляет наблюдателя в список. Вызов небезопасен для одновременного
// использования с Notify — если нужно добавлять наблюдателей во время работы,
// вызывайте Subscribe до начала обработки запросов.
func (c *Central) Subscribe(h EventHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers = append(c.handlers, h)
}

// Notify уведомляет всех подписанных наблюдателей о событии.
// Каждый наблюдатель вызывается в отдельной горутине через go h.Handle(event).
// Если список наблюдателей пуст или central равен nil, метод не паникует.
//
// Event передается по значению (копируется), поэтому наблюдатели могут менять его поля.
func (c *Central) Notify(event Event) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, h := range c.handlers {
		go h.Handle(event)
	}
}
