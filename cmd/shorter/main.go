package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"study-go.ru/cho/eto/internal/handler"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	addr := flag.String("a", ":8080", "адрес HTTP-сервера")
	flag.Parse()

	r := chi.NewRouter()

	// глобальные middleware
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	fmt.Print(`
╔══════════════════════════════╗
║        short-short           ║
╚══════════════════════════════╝
`)

	// регистрация обработчиков
	r.Post("/", handler.ShorterPost)
	r.Get("/{id}", handler.ShorterGet)

	fmt.Println("Запуск сервера на", *addr)
	log.Fatal(http.ListenAndServe(*addr, r))
}
