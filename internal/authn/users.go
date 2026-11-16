package authn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// userExistsQuery — та же проверка существования пользователя, что выполняет HTTP-middleware.
const userExistsQuery = "SELECT 1 FROM usr WHERE id = $1"

// PostgresUsers проверяет присутствие пользователя в PostgreSQL.
type PostgresUsers struct {
	DB *sql.DB
}

// UserExists говорит, есть ли пользователь с идентификатором usrID. Отсутствующая строка —
// не ошибка: это «пользователя нет», а не сбой.
func (u PostgresUsers) UserExists(ctx context.Context, usrID int) (bool, error) {
	var one int

	err := u.DB.QueryRowContext(ctx, userExistsQuery, usrID).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("запрос пользователя %d: %w", usrID, err)
	default:
		return true, nil
	}
}
