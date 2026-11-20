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

// StatsTX — источник счётчиков для GET /api/internal/stats. Метод Exec читает оба
// счётчика последовательно в рамках одной транзакции: значения берутся из одного снимка
// и гарантированно согласованы между собой.
type StatsTX interface {
	Exec(ctx context.Context) (urls, users int64, err error)
}

// StatsReader — источник одного счётчика. Обёрнутый через StatsReaderAdapter, преобразуется
// в StatsTX: Exec вызывет Reader дважды по одному запросу. Для источников на основе sql.DB
// предпочтительнее использовать StatsFromDBWithTX, чтобы оба запроса шли в одной транзакции.
type StatsReader func(ctx context.Context, query string) (int64, error)

// StatsReaderAdapter оборачивает StatsReader в StatsTX: Exec вызывает Reader дважды
// по одному запросу в отдельной транзакции, которая ограничивается временем ожидания.
type StatsReaderAdapter struct {
	Reader StatsReader
}

// Exec обёрнутого StatsReader читает оба счётчика по отдельности и применяет timeout.
func (a *StatsReaderAdapter) Exec(ctx context.Context) (urls, users int64, err error) {
	ctx, cancel := context.WithTimeout(ctx, statsQueryTimeout)
	defer cancel()

	urls, err = a.Reader(ctx, statsURLsQuery)
	if err != nil {
		return 0, 0, fmt.Errorf("число сокращений: %w", err)
	}

	users, err = a.Reader(ctx, statsUsersQuery)
	if err != nil {
		return 0, 0, fmt.Errorf("число пользователей: %w", err)
	}

	return urls, users, nil
}

// StatsFromDB возвращает источник одного счётчика для соединения с базой.
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

// StatsFromDBWithTX создаёт источник счётчиков в виде единой транзакции. Оба запроса выполняются
// внутри REPEATABLE READ-транзакции с отдельным соединением из пула, поэтому снимок
// гарантирует согласованность счётчиков между таблицами usr и url_srv.
func StatsFromDBWithTX(db *sql.DB) StatsTX {
	if db == nil {
		return nil
	}

	return statsTX{db: db}
}

type statsTX struct{ db *sql.DB }

// Exec открывает транзакцию REPEATABLE READ, читает оба счётчика и фиксирует транзакцию.
func (tx statsTX) Exec(ctx context.Context) (urls, users int64, err error) {
	txn, err := tx.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return 0, 0, err
	}

	defer func() {
		if err != nil {
			_ = txn.Rollback()
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, statsQueryTimeout)
	defer cancel()

	err = txn.QueryRowContext(ctx, statsURLsQuery).Scan(&urls)
	if err != nil {
		return 0, 0, err
	}

	err = txn.QueryRowContext(ctx, statsUsersQuery).Scan(&users)
	if err != nil {
		return 0, 0, err
	}

	return urls, users, txn.Commit()
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
func ShorterStats(tx StatsTX) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := loadStats(r.Context(), tx)
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

// loadStats читает оба счётчика одним вызовом источника; ошибка записи соединения сюда не доходит.
func loadStats(ctx context.Context, tx StatsTX) (statsResponse, error) {
	if tx == nil {
		return statsResponse{}, errNoStatsSource
	}

	urls, users, err := tx.Exec(ctx)

	return statsResponse{URLs: urls, Users: users}, err
}
