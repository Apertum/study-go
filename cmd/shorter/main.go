package main

import (
	"log"
	"net/http"

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

	r := chi.NewRouter()
	// глобальные middleware
	r.Use(chimw.ClientIPFromRemoteAddr)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(internalMiddleware.GzipMiddleware)
	// регистрация обработчиков
	r.Post("/", handler.ShorterPost(store))
	r.Post("/api/shorten", handler.ShorterPost(store))
	r.Get("/{id}", handler.ShorterGet(store))

	logrus.Debug("Запуск сервера на ", config.Addr)
	logrus.Debug("Base url: ", config.BaseURL)
	log.Fatal(http.ListenAndServe(config.Addr, r))
}
