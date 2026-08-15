package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	config "study-go.ru/cho/eto/internal/config"
	"study-go.ru/cho/eto/internal/handler"
	internalMiddleware "study-go.ru/cho/eto/internal/middleware"
	"study-go.ru/cho/eto/internal/storage"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
)

func init() {
	config.LogsInit()
	config.ParseFlags()
}

func main() {
	logrus.Info(`
╔═══════════════════════════════╗
║        short-short F          ║
╚═══════════════════════════════╝
    `)

	// загружаем данные из файла (если существует)
	store := storage.New(config.FileName)

	// Инициализируем БД один раз
	db, err := initDB(config.DatabaseDSN)
	if err != nil {
		logrus.Error("Failed to initialize database:", err)
	}


	r := chi.NewRouter()
	// глобальные middleware
	r.Use(chimw.ClientIPFromRemoteAddr)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(internalMiddleware.GzipMiddleware)

	// middleware для авторизации
	authMiddleware := handler.AuthMiddleware(db)

	// регистрация обработчиков
	r.Post("/ping", handler.ShorterPing(db))
	r.Post("/login", handler.ShorterLoginPost(db))
	// защищённые маршруты — требуют авторизованную куку
	r.Handle("/", authMiddleware(handler.ShorterPost(store)))
	r.Handle("/api/shorten", authMiddleware(handler.ShorterPost(store)))
	r.Handle("/api/shorten/batch", authMiddleware(handler.ShorterBatchPost(store)))
	r.Handle("/api/user/urls", authMiddleware(handler.ShorterUserURLsGet(db)))
	r.Get("/{id}", handler.ShorterGet(store))

	logrus.Debug("Запуск сервера на ", config.Addr)
	logrus.Debug("Base url: ", config.BaseURL)
	logrus.Fatal(http.ListenAndServe(config.Addr, r))
}

func initDB(dsn string) (*sql.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_DSN is empty")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Настраиваем пул соединений
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Проверяем, что БД действительно доступна
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("database ping failed: %w", err)
	}

	return db, nil
}
