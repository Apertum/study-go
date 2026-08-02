package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"

	_ "github.com/lib/pq"
)

func main() {
	var cntStr string = "postgres://postgres:postgres@localhost:5432/local_dev?sslmode=disable"
	db, err := sql.Open("postgres", cntStr)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// делаем запрос
	row0 := db.QueryRowContext(context.Background(), "SELECT COUNT(*) as count FROM videos")
	// готовим переменную для чтения результата
	var id int64
	err = row0.Scan(&id) // разбираем результат
	if err != nil {
		panic(err)
	}
	fmt.Println("query res = " + strconv.FormatInt(id, 10))

	row := db.QueryRowContext(context.Background(), "SELECT title, likes, comments_disabled FROM videos ORDER BY likes DESC LIMIT 1")
	var (
		title  string
		likes  int
		comdis bool
	)
	// порядок переменных должен соответствовать порядку колонок в запросе
	err = row.Scan(&title, &likes, &comdis)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s | %d | %t \r\n", title, likes, comdis)

	var list []Video
	list, err = QueryVideos(context.Background(), db, 5)
	if err != nil {
		panic(err)
	} else {
		for _, v := range list {
			length := 4
			if len(v.Tags) < length {
				length = len(v.Tags)
			}
			var tags string = strings.Join(v.Tags[:length], " # ")
			fmt.Println("title=\"" + v.Title + "\" ### " + tags)
		}

	}
}

type Tags []string

func (tags *Tags) Scan(value interface{}) error {
	// если `value` равен `nil`, будет возвращён пустой массив
	if value == nil {
		*tags = Tags{}
		return nil
	}

	sv, err := driver.String.ConvertValue(value)
	if err != nil {
		return fmt.Errorf("cannot scan value. %w", err)
	}

	v, ok := sv.(string)
	if !ok {
		return errors.New("cannot scan value. cannot convert value to string")
	}
	*tags = strings.Split(v, "|")

	// удаляем кавычки у тегов
	for i, v := range *tags {
		(*tags)[i] = strings.Trim(v, `"`)
	}
	return nil
}

// Value — функция, реализующая интерфейс driver.Valuer
func (tags Tags) Value() (driver.Value, error) {
	// преобразуем []string в string
	if len(tags) == 0 {
		return "", nil
	}
	return strings.Join(tags, "|"), nil
}

// Video — структура видео.
type Video struct {
	Id    string
	Title string
	Views int64
	Tags  Tags
}

// limit — максимальное количество записей.
const limit = 20

func QueryVideos(ctx context.Context, db *sql.DB, limit int) ([]Video, error) {
	videos := make([]Video, 0, limit)

	rows, err := db.QueryContext(ctx, "SELECT video_id, title, views, tags from videos ORDER BY views LIMIT $1", limit)
	if err != nil {
		return nil, err
	}

	// обязательно закрываем перед возвратом функции
	defer rows.Close()

	// пробегаем по всем записям
	for rows.Next() {
		var v Video
		err = rows.Scan(&v.Id, &v.Title, &v.Views, &v.Tags)
		if err != nil {
			return nil, err
		}
		videos = append(videos, v)
	}

	// проверяем на ошибки
	err = rows.Err()
	if err != nil {
		return nil, err
	}
	return videos, nil
}
