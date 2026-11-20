package shorten

import (
	"context"
	"database/sql"
	"fmt"
)

// userURLsQuery — список ссылок пользователя. Сортировка по id задаёт тот же порядок
// выдачи, что ответ GET /api/user/urls.
const userURLsQuery = "SELECT short_url, original_url FROM url_srv WHERE usr_id = $1 ORDER BY id"

// PostgresUserURLReader читает список ссылок пользователя из PostgreSQL.
type PostgresUserURLReader struct {
	DB *sql.DB
}

// List возвращает пары короткой и оригинальной ссылок пользователя в порядке создания.
// Пустой результат — не ошибка: вызывающий код сам решает, чем он для его транспорта
// (HTTP отвечает 204, gRPC — пустым списком).
func (r PostgresUserURLReader) List(ctx context.Context, usrID int) ([]Entry, error) {
	rows, err := r.DB.QueryContext(ctx, userURLsQuery, usrID)
	if err != nil {
		return nil, fmt.Errorf("%w: запрос списка ссылок: %w", ErrStorage, err)
	}
	defer rows.Close()

	var urls []Entry

	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.ShortURL, &entry.OriginalURL); err != nil {
			return nil, fmt.Errorf("%w: чтение строки списка ссылок: %w", ErrStorage, err)
		}

		urls = append(urls, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: обход строк списка ссылок: %w", ErrStorage, err)
	}

	return urls, nil
}
