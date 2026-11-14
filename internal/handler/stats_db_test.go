package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// envStatsTestDSN — способ указать тесту настоящий PostgreSQL. Без переменной тест пропускается:
// фильтр удалённых и отсутствие кэша имеет смысл проверять только против реальной схемы, а стенд
// поднимается docker-compose.yml, таблицы — миграциями migrations/000003_*.sql и 000004_*.sql.
const envStatsTestDSN = "TEST_DATABASE_DSN"

// TestShorterStatsCountsAgainstDatabase сверяет ответ эндпоинта с содержимым базы: созданное
// сокращение увеличивает urls на единицу, помеченное удалённым — в счётчик не попадает, хотя строка
// остаётся в таблице. Ответ читается одним и тем же обработчиком, поэтому рост и откат счётчиков
// подтверждают и отсутствие кэша между запросами.
//
// Счётчики сравниваются приращениями: в общей базе параллельные записи других процессов могут
// сдвинуть абсолютные значения.
func TestShorterStatsCountsAgainstDatabase(t *testing.T) {
	dsn := os.Getenv(envStatsTestDSN)
	if dsn == "" {
		t.Skipf("переменная %s не задана: проверка требует PostgreSQL и применённых миграций", envStatsTestDSN)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("соединение не настроено: %v", err)
	}

	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if pingErr := db.PingContext(ctx); pingErr != nil {
		t.Skipf("PostgreSQL недоступен: %v", pingErr)
	}

	statsHandler := ShorterStats(StatsFromDB(db))

	base := readCounts(t, statsHandler)

	fixture := "stats-" + t.Name()

	var userID int

	err = db.QueryRowContext(ctx, `INSERT INTO usr (usr_name) VALUES ($1) RETURNING id`, fixture).Scan(&userID)
	if err != nil {
		t.Fatalf("пользователь не создан: %v", err)
	}

	t.Cleanup(func() {
		if _, delErr := db.Exec(`DELETE FROM usr WHERE id = $1`, userID); delErr != nil {
			t.Errorf("пользователь фикстуры не удалён: %v", delErr)
		}
	})

	var urlID int

	err = db.QueryRowContext(ctx,
		`INSERT INTO url_srv (uuid, original_url, short_url, usr_id, deleted) VALUES ($1, $2, $3, $4, false) RETURNING id`,
		time.Now().UnixNano(), "https://example.invalid/"+fixture, fixture+"-short", userID,
	).Scan(&urlID)
	if err != nil {
		t.Fatalf("сокращение не создано: %v", err)
	}

	t.Cleanup(func() {
		if _, delErr := db.Exec(`DELETE FROM url_srv WHERE id = $1`, urlID); delErr != nil {
			t.Errorf("сокращение фикстуры не удалён: %v", delErr)
		}
	})

	active := readCounts(t, statsHandler)

	if active.URLs != base.URLs+1 {
		t.Errorf("urls %d после создания сокращения, ожидали %d", active.URLs, base.URLs+1)
	}

	if active.Users != base.Users+1 {
		t.Errorf("users %d после создания пользователя, ожидали %d", active.Users, base.Users+1)
	}

	if _, updErr := db.ExecContext(ctx, `UPDATE url_srv SET deleted = true WHERE id = $1`, urlID); updErr != nil {
		t.Fatalf("сокращение не помечено удалённым: %v", updErr)
	}

	softDeleted := readCounts(t, statsHandler)

	if softDeleted.URLs != base.URLs {
		t.Errorf("urls %d после мягкого удаления, ожидали %d", softDeleted.URLs, base.URLs)
	}

	if softDeleted.Users != base.Users+1 {
		t.Errorf("users %d после мягкого удаления сокращения, ожидали %d", softDeleted.Users, base.Users+1)
	}
}

// readCounts читает ответ эндпоинта тем же обработчиком: каждый вызов — новый запрос к базе.
func readCounts(t *testing.T, statsHandler http.HandlerFunc) statsResponse {
	t.Helper()

	rec := httptest.NewRecorder()

	statsHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/internal/stats", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, ожидали 200: %s", rec.Code, rec.Body.String())
	}

	var counts statsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &counts); err != nil {
		t.Fatalf("тело %q не разбирается: %v", rec.Body.String(), err)
	}

	return counts
}
