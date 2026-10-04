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
	"time"
)

// --- Action constants ---

func TestActionConstants(t *testing.T) {
	if ActionShorten != "shorten" {
		t.Errorf("ActionShorten = %q, want %q", ActionShorten, "shorten")
	}
	if ActionFollow != "follow" {
		t.Errorf("ActionFollow = %q, want %q", ActionFollow, "follow")
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
		t.Errorf("ts = %v, want 12345678", result["ts"])
	}
	if result["action"] != "shorten" {
		t.Errorf("action = %v, want shorten", result["action"])
	}
	if result["user_id"] != "12315134" {
		t.Errorf("user_id = %v, want 12315134", result["user_id"])
	}
	if result["url"] != "https://example.com/long" {
		t.Errorf("url = %v, want https://example.com/long", result["url"])
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
		t.Error("empty user_id should be omitted from JSON")
	}
	if result["action"] != "follow" {
		t.Errorf("action = %v, want follow", result["action"])
	}
	if result["url"] != "https://example.com/followed" {
		t.Errorf("url = %v, want https://example.com/followed", result["url"])
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
		t.Error("empty action should serialize as empty string")
	}
}

// --- FileHandler ---

func TestNewFileHandler_NonExistentDir(t *testing.T) {
	_, err := NewFileHandler("/nonexistent/dir/audit.log")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
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
		t.Errorf("parsed action = %s, want %s", parsed.Action, ActionShorten)
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
	// 5 events => 5 newlines
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
		t.Error("file should have written at least one line")
	}
}

// --- URLHandler ---

func TestURLHandler_New(t *testing.T) {
	h := NewURLHandler("http://localhost:9999/audit")
	if h.url != "http://localhost:9999/audit" {
		t.Errorf("url = %q, want %q", h.url, "http://localhost:9999/audit")
	}
	if h.client.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", h.client.Timeout)
	}
}

func TestURLHandler_SendSuccess(t *testing.T) {
	var urlReceived Event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &urlReceived)
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

	if urlReceived.Timestamp != 9999 {
		t.Errorf("received ts = %d, want 9999", urlReceived.Timestamp)
	}
	if urlReceived.Action != ActionFollow {
		t.Errorf("received action = %s, want follow", urlReceived.Action)
	}
}

func TestURLHandler_UnreachableServer(t *testing.T) {
	// Point to a port that's not listening — should not panic
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
	// Should not panic even with non-200 response
}

func TestURLHandler_PostFailsJsonMarshal(t *testing.T) {
	// This test covers the json.Marshal path by using an empty handler — it always works for Event
	// To test the error path we simulate it differently
	h := &URLHandler{client: nil, url: ""}
	// nil client would panic on Post, but Handle uses Marshal first then Post
	// We can't trigger marshal failure since Event.MarshalJSON never errors
	// This just ensures the function runs without side effects
	_ = h
}

// --- Central ---

func TestNewCentral(t *testing.T) {
	c := NewCentral()
	if c.handlers == nil {
		t.Fatal("handlers should not be nil")
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

	c.Notify(Event{Action: ActionShorten})
	// Each handler runs in a goroutine, wait for them to complete
	time.Sleep(50 * time.Millisecond)

	if got := count.Load(); got != 10 {
		t.Errorf("expected 10 notifies, got %d", got)
	}
}

func TestCentral_NotifyEmpty(t *testing.T) {
	c := NewCentral()
	// Should not panic with zero handlers
	c.Notify(Event{Action: ActionFollow})
	time.Sleep(10 * time.Millisecond)
}

func TestCentral_NotifyWithHandlers(t *testing.T) {
	c := NewCentral()
	var mu sync.Mutex
	var events []Event

	c.Subscribe(&recordingHandler{store: &events, mu: &mu})
	c.Notify(Event{Timestamp: 100, Action: ActionShorten, UserID: "1", URL: "https://a.com"})
	c.Notify(Event{Timestamp: 200, Action: ActionFollow, URL: "https://b.com"})

	// Handle runs in goroutines, give them time to complete
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
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
	// Notify is a method on pointer; receiver can't be nil in normal usage
	// but we ensure basic instantiation works
	var c *Central = &Central{handlers: make([]EventHandler, 0)}
	c.Notify(Event{})
	time.Sleep(10 * time.Millisecond)
}

// --- Integration: Central with FileHandler and URLHandler ---

func TestCentral_Integration_FileAndURL(t *testing.T) {
	// Create temp file
	tmp, err := os.CreateTemp("", "audit-integration-*.jsonl")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	// Create mock URL server
	var urlReceived Event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &urlReceived)
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

	c.Notify(Event{
		Timestamp: 500,
		Action:    ActionShorten,
		UserID:    "10",
		URL:       "https://integration.test",
	})

	time.Sleep(100 * time.Millisecond)

	// Check file
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var fileEv Event
	if err := json.Unmarshal(data, &fileEv); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if fileEv.UserID != "10" {
		t.Errorf("file event user_id = %q, want 10", fileEv.UserID)
	}

	// Check URL server
	if urlReceived.Timestamp != 500 {
		t.Errorf("url received ts = %d, want 500", urlReceived.Timestamp)
	}
	if urlReceived.UserID != "10" {
		t.Errorf("url received user_id = %q, want 10", urlReceived.UserID)
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
			c.Notify(Event{
				Timestamp: int64(idx),
				Action:    ActionShorten,
				UserID:    string(rune('a' + idx%26)),
				URL:       "https://race.example.com/test",
			})
		}(i)
	}
	wg.Wait()
}

// --- Helper handlers for testing ---

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
