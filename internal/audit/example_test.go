package audit_test

import (
	"encoding/json"
	"fmt"
	"os"

	"study-go.ru/cho/eto/internal/audit"
)

// Пример создания Central и подписки FileHandler.
// Создаёт временный файл, подписывает его как приёмник аудита,
// генерирует событие и проверяет запись в файл.
func ExampleNewFileHandler() {
	// Создаём временный файл для аудита
	tmpFile, err := os.CreateTemp("", "example-audit-*.jsonl")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Создаём FileHandler и подписываем на Central
	handler, err := audit.NewFileHandler(tmpPath)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer handler.Close()

	central := audit.NewCentral()
	central.Subscribe(handler)

	// Генерируем событие (блокируем до завершения всех обработчиков)
	event := audit.Event{
		Timestamp: 1700000000,
		Action:    audit.ActionShorten,
		UserID:    "42",
		URL:       "https://example.com/very/long/path",
	}
	central.NotifySynced(event)

	// Читаем записанное событие из файла
	data, _ := os.ReadFile(tmpPath)
	var recorded audit.Event
	json.Unmarshal(data, &recorded)

	fmt.Printf("Action: %s\n", recorded.Action)
	fmt.Printf("User ID: %s\n", recorded.UserID)
	fmt.Printf("URL: %s\n", recorded.URL)

	// Выход:
	// Action: shorten
	// User ID: 42
	// URL: https://example.com/very/long/path
}

// Пример работы с Action константами.
// Демонстрирует использование ActionShorten и ActionFollow.
func ExampleAction() {
	shortenAction := audit.ActionShorten
	followAction := audit.ActionFollow

	fmt.Printf("Shorten action: %q\n", shortenAction)
	fmt.Printf("Follow action: %q\n", followAction)

	// Выход:
	// Shorten action: "shorten"
	// Follow action: "follow"
}

// Пример Event MarshalJSON с пустым user_id.
// Показывает, что поле user_id исключается из JSON при пустом значении.
func ExampleEvent_MarshalJSON() {
	// С пустым user_id — поле исключается
	ev1 := audit.Event{
		Timestamp: 100,
		Action:    audit.ActionFollow,
		URL:       "https://example.com/followed",
	}
	data1, _ := json.Marshal(ev1)
	fmt.Println("Without user_id:", string(data1))

	// С заполненным user_id — поле присутствует
	ev2 := audit.Event{
		Timestamp: 200,
		Action:    audit.ActionShorten,
		UserID:    "123",
		URL:       "https://example.com/shortened",
	}
	data2, _ := json.Marshal(ev2)
	fmt.Println("With user_id:", string(data2))

	// Выход:
	// Without user_id: {"ts":100,"action":"follow","url":"https://example.com/followed"}
	// With user_id: {"ts":200,"action":"shorten","user_id":"123","url":"https://example.com/shortened"}
}

// Пример подписки нескольких наблюдателей.
// Централь регистрирует FileHandler и URLHandler (через mock).
// После уведомления оба получают событие.
func ExampleCentral_Subscribe() {
	tmpFile, _ := os.CreateTemp("", "multi-audit-*.jsonl")
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	central := audit.NewCentral()

	fileH, _ := audit.NewFileHandler(tmpPath)
	central.Subscribe(fileH)
	defer fileH.Close()

	event := audit.Event{
		Timestamp: 999,
		Action:    audit.ActionFollow,
		URL:       "https://example.com/demo",
	}
	central.NotifySynced(event)

	data, _ := os.ReadFile(tmpPath)
	var ev audit.Event
	json.Unmarshal(data, &ev)

	fmt.Printf("Event: ts=%d action=%s url=%s\n", ev.Timestamp, ev.Action, ev.URL)

	// Выход: Event: ts=999 action=follow url=https://example.com/demo
}
