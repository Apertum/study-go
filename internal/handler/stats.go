package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// statsQueryTimeout — потолок ожидания обоих счётчиков. Отсчёт идёт от контекста запроса, поэтому
// клиент, оборвавший соединение, освобождает соединение с базой раньше срока.
const statsQueryTimeout = 3 * time.Second

// Счётчики считаются в SQL: в режиме файла их посчитать негде, а условие «не удалено» живёт рядом
// с данными. Таблицы взяты из table.sql и internal/storage/postgres.
const (
	statsURLsQuery  = "SELECT count(*) FROM url_srv WHERE NOT deleted"
	statsUsersQuery = "SELECT count(*) FROM usr"
)

// errNoStatsSource — источник счётчиков не задан; причиной отвечает 503, а не паникой.
var errNoStatsSource = errors.New("источник счётчиков не задан")

// StatsReader — источник счётчиков для GET /api/internal/stats. Зависимость намеренно уже, чем
// весь слой хранения: счётчики идут напрямую в базу, потому что у файтового хранилища нет ни
// таблицы пользователей, ни признака удаления. Одна функция вместо интерфейса: единственное
// действие источника — выполнить запрос и вернуть число; *sql.Row со значением не построить,
// поэтому готовое int64 избавляет двойников тестов от прослойки для строки.
type StatsReader func(ctx context.Context, query string) (int64, error)

// StatsFromDB возвращает читателя счётчиков для соединения с базой.
//
// Нулевое соединение отдаёт как nil, а не как обёртку над ним: у функции nil и есть nil,
// поэтому проверка нулевого источника в loadStats не обманывается указателем внутри интерфейса.
// Сервис продолжает работать без базы, поэтому отсутствие источника — штатный случай, отвечающий 503.
func StatsFromDB(db *sql.DB) StatsReader {
	if db == nil {
		return nil
	}

	return func(ctx context.Context, query string) (int64, error) {
		var count int64

		err := db.QueryRowContext(ctx, query).Scan(&count)

		return count, err
	}
}

// statsResponse — тело ответа эндпоинта: ровно два числовых поля в указанном порядке.
type statsResponse struct {
	URLs  int64 `json:"urls"`
	Users int64 `json:"users"`
}

// ShorterStats отвечает числом активных сокращений и числом пользователей.
//
// Счётчики читаются на каждый запрос без кэша, чтобы ответ совпадал с содержимым базы сразу после
// создания или удаления ссылки. Доступность источника проверяется здесь, а доступ из доверенной
// подсети — в TrustedSubnetGate до вызова обработчика: отказ по сети приходит раньше, чем станет
// известно о состоянии базы.
//
// 200 — оба запроса выполнены, 0 — корректное значение пустой базы. 503 — источник не задан,
// запрос или разбор значения не удались либо срок ожидания истёк; тело в этом случае пустое,
// чтобы эндпоинт не стал способом проверить содержимое базы или её доступность.
func ShorterStats(reader StatsReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := loadStats(r.Context(), reader)
		if err != nil {
			logrus.WithError(err).Warn("статистика недоступна")

			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(stats); err != nil {
			logrus.WithError(err).Warn("ответ статистики не записан")
		}
	}
}

// loadStats читает оба счётчика одним сроком ожидания на пару. Кодировка ответа — дело
// обработчика, поэтому ошибка записи соединения сюда не доходит.
func loadStats(ctx context.Context, reader StatsReader) (statsResponse, error) {
	if reader == nil {
		return statsResponse{}, errNoStatsSource
	}

	ctx, cancel := context.WithTimeout(ctx, statsQueryTimeout)
	defer cancel()

	urls, err := reader(ctx, statsURLsQuery)
	if err != nil {
		return statsResponse{}, fmt.Errorf("число сокращений: %w", err)
	}

	users, err := reader(ctx, statsUsersQuery)
	if err != nil {
		return statsResponse{}, fmt.Errorf("число пользователей: %w", err)
	}

	return statsResponse{URLs: urls, Users: users}, nil
}
