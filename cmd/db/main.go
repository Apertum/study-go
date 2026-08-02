package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
	"study-go.ru/cho/eto/internal/config"
)

func main() {
	// установим уровень логирования
	config.LogsInit()

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
		likes  *int
		comdis *bool
	)
	// порядок переменных должен соответствовать порядку колонок в запросе
	err = row.Scan(&title, &likes, &comdis)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		panic(err)
	} else {
		fmt.Printf("%s | %d | %t \r\n", title, likes, comdis)
	}

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

	ctx := context.Background()
	_, err = db.ExecContext(ctx, `CREATE TABLE if not exists videos (
        "video_id" TEXT,
        "title" TEXT,
        "publish_time" TEXT,
        "tags" TEXT,
        "views" INTEGER
      )`)
	if err != nil {
		logrus.Fatal(err)
		panic(err)
	}

	if 1 == 2 {
		// читаем записи из файла в слайс []Video вспомогательной функцией
		videos, err := readVideoCSV("E:\\GO\\projects\\stady-go\\USvideos.csv")
		if err != nil {
			logrus.Fatal(err)
		}
		// записываем []Video в базу данных
		// тоже вспомогательной функцией
		err = insertVideos(context.Background(), db, videos)
		if err != nil {
			logrus.Fatal(err)
		}
		fmt.Printf("Всего csv-записей %v\n", len(videos))
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
	Id          string
	Title       string
	Views       int64
	Tags        Tags
	PublishTime time.Time // publish_time
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

func readVideoCSV(csvFile string) ([]Video, error) {
	// открываем csv-файл
	file, err := os.Open(csvFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var videos []Video

	// определим индексы нужных полей
	const (
		Id          = 0 // video_id
		Title       = 2 // title
		PublishTime = 5 // publish_time
		Tags        = 6 // tags
		Views       = 7 // views
	)

	// конструируем Reader из пакета encoding/csv
	// он умеет читать строки csv-файла
	r := csv.NewReader(file)
	// пропустим первую строку с именами полей
	if _, err := r.Read(); err != nil {
		return nil, err
	}

	for {
		// csv.Reader за одну операцию Read() считывает одну csv-запись
		l, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		// инициализируем целевую структуру,
		// в которую будем делать разбор csv-записи
		v := Video{
			Id:    l[Id],
			Title: l[Title],
		}
		// парсинг строковых записей в типизированные поля структуры
		if v.PublishTime, err = time.Parse(time.RFC3339, l[PublishTime]); err != nil {
			return nil, err
		}
		tags := strings.Split(l[Tags], "|")
		for i, v := range tags {
			tags[i] = strings.Trim(v, `"`)
		}
		v.Tags = tags
		if v.Views, err = strconv.ParseInt(l[Views], 10, 64); err != nil {
			return nil, err
		}
		// добавляем полученную структуру в слайс
		videos = append(videos, v)
	}
	return videos, nil
}

func insertVideos(ctx context.Context, db *sql.DB, videos []Video) error {
	// начинаем транзакцию
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	for _, v := range videos {
		logrus.Debug("v.Id = " + v.Id + ";  v.Title=" + v.Title + ";  PublishTime=" + v.PublishTime.Format("2006-01-02 15:04:05") + ";  Tags=" + strings.Join(v.Tags, `|`) + ";  Views=" + strconv.FormatInt(v.Views, 10))
		_, err := tx.ExecContext(ctx,
			"INSERT INTO videos (video_id, title, publish_time, tags, views) VALUES($1, $2, $3, $4, $5)",
			v.Id, v.Title, v.PublishTime, strings.Join(v.Tags, `|`), v.Views)
		if err != nil {
			logrus.Error(err)
			// если ошибка, то откатываем изменения
			tx.Rollback()
			return err
		}
	}

	// завершаем транзакцию
	return tx.Commit()
}
