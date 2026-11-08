package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// gateHandler — подписчик, который сообщает о полученном событии и не разбирает его, пока
// не откроют шлюз. Им проверяют, что Notify не блокирует хендлер и что Wait действительно
// дожидается фактической доставки.
type gateHandler struct {
	came    chan Event
	release chan struct{}
	mu      sync.Mutex
	handled int
}

func newGateHandler(capacity int) *gateHandler {
	return &gateHandler{
		came:    make(chan Event, capacity),
		release: make(chan struct{}),
	}
}

func (h *gateHandler) Handle(event Event) {
	h.came <- event
	<-h.release

	h.mu.Lock()
	defer h.mu.Unlock()
	h.handled++
}

// processed возвращает число разобранных событий.
func (h *gateHandler) processed() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.handled
}

// waitGoroutines даёт фоновым горутинам завершиться и проверяет, что их стало не больше
// исходного числа: ожидание и обработка не должны оставлять текущих горутин.
func waitGoroutines(t *testing.T, baseline int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)

	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := runtime.NumGoroutine(); got > baseline {
		t.Errorf("горутин %d, ожидалось не больше %d", got, baseline)
	}
}

// readAuditLines возвращает непустые строки файла аудита.
func readAuditLines(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("файл аудита не читается: %v", err)
	}

	var out []string

	for line := range strings.SplitSeq(string(data), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}

	return out
}

// TestCentralNotifyDoesNotWaitForHandlers фиксирует неблокирующий характер Notify: он
// возвращается, когда обработчик уже стоит посреди разбора события.
func TestCentralNotifyDoesNotWaitForHandlers(t *testing.T) {
	c := NewCentral()
	h := newGateHandler(1)
	c.Subscribe(h)

	c.Notify(Event{Timestamp: 1, Action: ActionShorten, URL: "https://notify.example"})

	<-h.came

	if got := h.processed(); got != 0 {
		t.Fatalf("разобрано %d событий, ожидалось 0: Notify дождался подписчика", got)
	}

	close(h.release)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Wait(ctx); err != nil {
		t.Fatalf("Wait не дождался разобранного события: %v", err)
	}

	if got := h.processed(); got != 1 {
		t.Fatalf("разобрано %d событий, ожидалось 1", got)
	}
}

// TestCentralWaitWaitsForDeliveredEvents проверяет обе ветки Wait: ошибку по истечении
// контекста и nil после разбора всех поставленных событий.
func TestCentralWaitWaitsForDeliveredEvents(t *testing.T) {
	const events = 5

	baseline := runtime.NumGoroutine()

	c := NewCentral()
	gated := newGateHandler(events)
	c.Subscribe(gated)
	c.Subscribe(&trackingHandler{})

	for i := range events {
		c.Notify(Event{Timestamp: int64(700 + i), Action: ActionShorten, URL: "https://wait.example"})
	}

	for range events {
		<-gated.came
	}

	shortCtx, cancelShort := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelShort()

	err := c.Wait(shortCtx)
	if err == nil {
		t.Fatal("Wait вернулся успешно, хотя ни одно событие не разобрано")
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ошибка Wait = %v, ожидался %v", err, context.DeadlineExceeded)
	}

	close(gated.release)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Wait(ctx); err != nil {
		t.Fatalf("Wait после открытия шлюза: %v", err)
	}

	if got := gated.processed(); got != events {
		t.Fatalf("разобрано %d событий, ожидалось %d", got, events)
	}

	waitGoroutines(t, baseline)
}

// TestCentralWaitWithoutHandlers: пустой список подписчиков — остановке нечего ждать.
func TestCentralWaitWithoutHandlers(t *testing.T) {
	c := NewCentral()

	c.Notify(Event{Timestamp: 3, Action: ActionFollow, URL: "https://empty.example"})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := c.Wait(ctx); err != nil {
		t.Fatalf("Wait без подписчиков: %v", err)
	}
}

// TestWaitDeliversToFileBeforeClose — связка «настоящий FileHandler + Central.Wait»: после
// Wait каждое поставленное событие лежит в файле, а закрытый приёмник больше ничего не
// дописывает.
func TestWaitDeliversToFileBeforeClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")

	fileH, err := NewFileHandler(path)
	if err != nil {
		t.Fatalf("NewFileHandler: %v", err)
	}

	const events = 4

	c := NewCentral()
	c.Subscribe(fileH)

	for i := range events {
		c.Notify(Event{
			Timestamp: int64(900 + i),
			Action:    ActionShorten,
			UserID:    "7",
			URL:       fmt.Sprintf("https://stop.example/%d", i),
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Wait(ctx); err != nil {
		t.Fatalf("Wait до закрытия приёмника: %v", err)
	}

	if err := fileH.Close(); err != nil {
		t.Fatalf("Close приёмника: %v", err)
	}

	lines := readAuditLines(t, path)
	if len(lines) != events {
		t.Fatalf("в файле %d строк, ожидалось %d", len(lines), events)
	}

	seen := make(map[int64]bool, events)

	for _, line := range lines {
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("строка %q не разобрана: %v", line, err)
		}

		seen[ev.Timestamp] = true
	}

	for i := range events {
		if !seen[int64(900+i)] {
			t.Errorf("событие с ts=%d отсутствует в файле аудита", 900+i)
		}
	}

	// Приёмник, закрытый раньше ожидания, потерял бы эту запись; здесь она не появляется.
	c.Notify(Event{Timestamp: 999, Action: ActionFollow, URL: "https://after-close.example"})

	if err := c.Wait(ctx); err != nil {
		t.Fatalf("Wait после закрытия приёмника: %v", err)
	}

	if after := readAuditLines(t, path); len(after) != events {
		t.Errorf("в закрытый файл дописано %d строк", len(after)-events)
	}
}
