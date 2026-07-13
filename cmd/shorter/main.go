package main

import (
	"log"
	"net/http"

	config "study-go.ru/cho/eto/internal/config"
	"study-go.ru/cho/eto/internal/handler"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
)

func init() {
	config.LogsInit()
	config.ParseFlags()
}

func main() {
	logrus.Info(`
╔══════════════════════════════╗
║        short-short           ║
╚══════════════════════════════╝
    `)

	r := chi.NewRouter()
	// глобальные middleware
	r.Use(middleware.ClientIPFromRemoteAddr)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// регистрация обработчиков
	r.Post("/", handler.ShorterPost)
	r.Get("/{id}", handler.ShorterGet)

	logrus.Debug("Запуск сервера на ", config.Addr)
	logrus.Debug("Base url: ", config.BaseUrl)
	log.Fatal(http.ListenAndServe(config.Addr, r))
}
