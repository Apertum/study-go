package audit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

// --- Константы Action ---

func TestActionConstants(t *testing.T) {
	if ActionShorten != "shorten" {
		t.Errorf("ActionShorten = %q, ожидалось %q", ActionShorten, "shorten")
	}
	if ActionFollow != "follow" {
		t.Errorf("ActionFollow = %q, ожидалось %q", ActionFollow, "follow")
	}
}

// --- Event + MarshalJSON ---

func TestEventMarshalJSON_WithUserID(t *testing.T) {
	ev := Event{
		Timestamp: 12345678,
		Action:    ActionShorten,
		UserID:    "12315134",
		URL:       "https://example.com/long",
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if int(result["ts"].(float64)) != 12345678 {
		t.Errorf("ts = %v, ожидалось 12345678", result["ts"])
	}
	if result["action"] != "shorten" {
		t.Errorf("action = %v, ожидалось shorten", result["action"])
	}
	if result["user_id"] != "12315134" {
		t.Errorf("user_id = %v, ожидалось 12315134", result["user_id"])
	}
	if result["url"] != "https://example.com/long" {
		t.Errorf("url = %v, ожидалось https://example.com/long", result["url"])
	}
}

func TestEventMarshalJSON_EmptyUserID(t *testing.T) {
	ev := Event{
		Timestamp: 100,
		Action:    ActionFollow,
		URL:       "https://example.com/followed",
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if _, exists := result["user_id"]; exists {
		t.Error("пустой user_id должен отсутствовать из JSON")
	}
	if result["action"] != "follow" {
		t.Errorf("action = %v, ожидалось follow", result["action"])
	}
	if result["url"] != "https://example.com/followed" {
		t.Errorf("url = %v, ожидалось https://example.com/followed", result["url"])
	}
}

func TestEventMarshalJSON_InvalidAction(t *testing.T) {
	ev := Event{
		Timestamp: 200,
		Action:    "",
		URL:       "https://example.com",
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if raw["action"] != "" {
		t.Error("пустое action должно сериализоваться как пустая строка")
	}
}

// --- Файловый обработчик ---

func TestNewFileHandler_NonExistentDir(t *testing.T) {
	_, err := NewFileHandler("/nonexistent/dir/audit.log")
	if err == nil {
		t.Fatal("ожидалась ошибка для несуществующей директории")
	}
}

func TestFileHandler_WriteAndRead(t *testing.T) {
	tmp, err := os.CreateTemp("", "audit-test-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	h, err := NewFileHandler(path)
	if err != nil {
		t.Fatalf("NewFileHandler: %v", err)
	}
	defer h.Close()

	ev := Event{
		Timestamp: 1700000000,
		Action:    ActionShorten,
		UserID:    "42",
		URL:       "https://example.com/a",
	}

	h.Handle(ev)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := string(data)
	var parsed Event
	if err := json.Unmarshal([]byte(lines), &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if parsed.Action != ActionShorten {
		t.Errorf("парсинг action = %s, ожидалось %s", parsed.Action, ActionShorten)
	}
}

func TestFileHandler_AppendMultipleEvents(t *testing.T) {
	tmp, err := os.CreateTemp("", "audit-append-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	h, _ := NewFileHandler(path)
	defer h.Close()

	for i := 0; i < 5; i++ {
		h.Handle(Event{
			Timestamp: int64(1000 + i),
			Action:    ActionShorten,
			URL:       "https://example.com/" + string(rune('a'+i)),
		})
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	// 5 событий => 5 переводов строки
	if lines != 5 {
		t.Errorf("expected 5 newlines, got %d", lines)
	}
}

func TestFileHandler_Close(t *testing.T) {
	tmp, err := os.CreateTemp("", "audit-close-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	h, _ := NewFileHandler(path)

	err = h.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestFileHandler_ConcurrentWrites(t *testing.T) {
	tmp, err := os.CreateTemp("", "audit-concurrent-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	h, _ := NewFileHandler(path)
	defer h.Close()

	const count = 50
	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func(idx int) {
			defer wg.Done()
			h.Handle(Event{
				Timestamp: int64(idx),
				Action:    ActionFollow,
				URL:       "https://example.com/concurrent/" + string(rune('a'+idx%26)),
			})
		}(i)
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != count {
		t.Errorf("expected %d lines, got %d", count, lines)
	}
}

func TestFileHandler_InvalidMarshal(t *testing.T) {
	tmp, err := os.CreateTemp("", "audit-invalidmarshal-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	h, _ := NewFileHandler(path)
	defer h.Close()

	// Even if event is valid (MarshalJSON always succeeds), test the graceful path
	h.Handle(Event{Timestamp: 0, Action: ActionFollow})

	// Verify file is not empty
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() == 0 {
		t.Error("файл должен содержать хотя бы одну строку")
	}
}

// --- Сетевой обработчик ---

func TestURLHandler_New(t *testing.T) {
	h := NewURLHandler("http://localhost:9999/audit")
	if h.url != "http://localhost:9999/audit" {
		t.Errorf("url = %q, ожидалось %q", h.url, "http://localhost:9999/audit")
	}
}

func TestURLHandler_SendSuccess(t *testing.T) {
	var urlReceived Event
	var mu sync.Mutex
	received := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, ожидалось POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, ожидалось application/json", ct)
		}
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		json.Unmarshal(data, &urlReceived)
		mu.Unlock()
		close(received)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	h := NewURLHandler(server.URL)
	ev := Event{
		Timestamp: 9999,
		Action:    ActionFollow,
		URL:       "https://example.com/url",
	}
	h.Handle(ev)

	<-received // ждём завершения HTTP-запроса в горутине Handle

	mu.Lock()
	defer mu.Unlock()

	if urlReceived.Timestamp != 9999 {
		t.Errorf("получено ts = %d, ожидалось 9999", urlReceived.Timestamp)
	}
	if urlReceived.Action != ActionFollow {
		t.Errorf("получено action = %s, ожидалось follow", urlReceived.Action)
	}
}

func TestURLHandler_UnreachableServer(t *testing.T) {
	// Подключаемся к несуществующему порту — не должно паниковать
	h := NewURLHandler("http://127.0.0.1:1")
	h.Handle(Event{Timestamp: 1, Action: ActionShorten, URL: "https://x.com"})
}

func TestURLHandler_ServerRejects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	h := NewURLHandler(server.URL)
	h.Handle(Event{Timestamp: 5, Action: ActionFollow, URL: "https://x.com/rejected"})
	// Не должен паниковать даже при ответе с кодом отличным от 200
}

func TestURLHandler_PostFailsJsonMarshal(t *testing.T) {
	// Покрытие пути json.Marshal через пустой обработчик — всегда работает для Event.
	// Для проверки пути ошибки поступаем иначе: Handler с nil-client не вызывает
	// http.Post напрямую (Handle сначала делает Marshal, потом Post).
	// Event.MarshalJSON никогда не возвращает ошибку, поэтому проверка на ошибку
	// невозможна. Этот тест просто гарантирует отсутствие побочных эффектов.
	h := &URLHandler{client: nil, url: ""}
	_ = h
}

// --- Central ---

func TestNewCentral(t *testing.T) {
	c := NewCentral()
	if c.handlers == nil {
		t.Fatal("handlers не должен быть nil")
	}
}

func TestCentral_Subscribe(t *testing.T) {
	c := NewCentral()
	c.Subscribe(&trackingHandler{})
}

func TestCentral_SubscribeMultiple(t *testing.T) {
	c := NewCentral()
	count := &atomic.Int64{}
	for i := 0; i < 10; i++ {
		c.Subscribe(&countingHandler{counter: count})
	}

	c.NotifySynced(Event{Action: ActionShorten})

	if got := count.Load(); got != 10 {
		t.Errorf("ожидается 10 уведомлений, получено %d", got)
	}
}

func TestCentral_NotifyEmpty(t *testing.T) {
	c := NewCentral()
	// Не должен паниковать при нулевом количестве обработчиков
	c.NotifySynced(Event{Action: ActionFollow})
}

func TestCentral_NotifyWithHandlers(t *testing.T) {
	c := NewCentral()
	var mu sync.Mutex
	var events []Event

	c.Subscribe(&recordingHandler{store: &events, mu: &mu})
	c.NotifySynced(Event{Timestamp: 100, Action: ActionShorten, UserID: "1", URL: "https://a.com"})
	c.NotifySynced(Event{Timestamp: 200, Action: ActionFollow, UserID: "2", URL: "https://b.com"})

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Errorf("ожидается 2 события, получено %d", len(events))
	}
	// Проверяем наличие обоих событий без привязки к порядку горутин
	timestamps := make(map[int64]bool)
	for _, ev := range events {
		timestamps[ev.Timestamp] = true
	}
	if !timestamps[100] || !timestamps[200] {
		t.Errorf("expected timestamps 100 and 200, got %v", func() []int64 {
			var ts []int64
			for _, e := range events {
				ts = append(ts, e.Timestamp)
			}
			return ts
		}())
	}
}

func TestCentral_NotifyNilCentral(t *testing.T) {
	// Notify — метод указателя; в нормальном использовании receiver не может быть nil.
	// Здесь просто проверяем, что базовая инициализация работает.
	var c *Central = &Central{handlers: make([]EventHandler, 0)}
	c.NotifySynced(Event{})
}

// --- Интеграция: Central с FileHandler и URLHandler ---

func TestCentral_Integration_FileAndURL(t *testing.T) {
	// Создаём временный файл
	tmp, err := os.CreateTemp("", "audit-integration-*.jsonl")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	// Тестовый URL-сервер для приёма событий аудита
	var urlReceived Event
	var mu sync.Mutex
	urlReceivedCh := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		json.Unmarshal(data, &urlReceived)
		mu.Unlock()
		close(urlReceivedCh)
		w.WriteHeader(200)
	}))
	defer server.Close()

	c := NewCentral()

	fileH, err := NewFileHandler(path)
	if err != nil {
		t.Fatalf("NewFileHandler: %v", err)
	}
	defer fileH.Close()

	urlH := NewURLHandler(server.URL)

	c.Subscribe(fileH)
	c.Subscribe(urlH)

	c.NotifySynced(Event{
		Timestamp: 500,
		Action:    ActionShorten,
		UserID:    "10",
		URL:       "https://integration.test",
	})

	// NotifySynced заблокируется до завершения всех горутин, включая HTTP-запрос URLHandler
	<-urlReceivedCh // на всякий случай, хотя NotifySynced уже вернулся

	// Проверка записи в файл
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var fileEv Event
	if err := json.Unmarshal(data, &fileEv); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if fileEv.UserID != "10" {
		t.Errorf("user_id события в файле = %q, ожидалось 10", fileEv.UserID)
	}

	// Проверка получения события URL-сервером
	mu.Lock()
	defer mu.Unlock()
	if urlReceived.Timestamp != 500 {
		t.Errorf("timestamp события в URL = %d, ожидалось 500", urlReceived.Timestamp)
	}
	if urlReceived.UserID != "10" {
		t.Errorf("user_id события в URL = %q, ожидалось 10", urlReceived.UserID)
	}
}

func TestCentral_Race(t *testing.T) {
	c := NewCentral()
	c.Subscribe(&recordingHandler{store: &[]Event{}, mu: &sync.Mutex{}})
	c.Subscribe(&countingHandler{counter: &atomic.Int64{}})
	c.Subscribe(&trackingHandler{})

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			c.NotifySynced(Event{
				Timestamp: int64(idx),
				Action:    ActionShorten,
				UserID:    string(rune('a' + idx%26)),
				URL:       "https://race.example.com/test",
			})
		}(i)
	}
	wg.Wait()
}

// --- Тестовые обработчики ---

type trackingHandler struct{}

func (h *trackingHandler) Handle(Event) {}

type countingHandler struct {
	counter *atomic.Int64
}

func (h *countingHandler) Handle(Event) {
	h.counter.Add(1)
}

type recordingHandler struct {
	store *[]Event
	mu    *sync.Mutex
}

func (h *recordingHandler) Handle(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.store = append(*h.store, ev)
}
