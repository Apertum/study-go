package storage_test

import (
	"fmt"

	"study-go.ru/cho/eto/internal/storage"
)

// Пример использования New для создания in-memory хранилища.
// Создает хранилище без бэкенда (без DATABASE_DSN), которое работает с in-memory картами.
func ExampleNew() {
	store := storage.New("")

	fmt.Printf("Storage created: %p\n", store)

	// Выход: Storage created: 0x...
}

// Пример использования PutUnique и Get.
// Создаёт запись, затем читает её по shortID.
func ExampleStorage_PutUnique() {
	store := storage.New("")

	uuid := store.NextID()
	shortURL := "http://short.ru/abc123"
	originalURL := "https://example.com/very/long/path/to/resource"
	userID := 1

	err := store.PutUnique(uuid, shortURL, originalURL, userID)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	foundShortURL, exists := store.GetByOriginalURL(originalURL)
	fmt.Printf("Found: short=%s exists=%v\n", foundShortURL, exists)

	// Выход: Found: short=http://short.ru/abc123 exists=true
}

// Пример использования NextID.
// Демонстрирует генерацию последовательных идентификаторов.
func ExampleStorage_NextID() {
	store := storage.New("")

	id1 := store.NextID()
	id2 := store.NextID()
	id3 := store.NextID()

	fmt.Printf("IDs: %s, %s, %s\n", id1, id2, id3)

	// Выход: IDs: 0, 1, 2
}

// Пример обработки дубликата оригинального URL через PutUnique.
// Пытается вставить тот же original_url дважды — вторая операция вернёт ошибку.
func ExampleStorage_PutUnique_duplicate() {
	store := storage.New("")

	uuid1 := store.NextID()
	uuid2 := store.NextID()
	shortURL1 := "http://short.ru/first"
	shortURL2 := "http://short.ru/second"
	originalURL := "https://example.com/duplicate"

	err1 := store.PutUnique(uuid1, shortURL1, originalURL, 1)
	fmt.Printf("First insert: error=%v\n", err1)

	err2 := store.PutUnique(uuid2, shortURL2, originalURL, 1)
	fmt.Printf("Duplicate insert: error=%v\n", err2 != nil)

	// Выход:
	// First insert: error=<nil>
	// Duplicate insert: error=true
}
