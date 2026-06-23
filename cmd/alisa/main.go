package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var (
    a *string
    width *int
    thumb *bool
)

func init() {
    // используем init-функцию
    a = flag.String("a2", ":8080", "alisa port")
    width = flag.Int("width", 1024, "width of the image")
    thumb = flag.Bool("thumb", false, "create thumb")
}

// функция main вызывается автоматически при запуске приложения
func main() {
    // обрабатываем аргументы командной строки
    parseFlags()
    fmt.Println("1 Running server on", flagRunAddr)
    fmt.Println("2 Running server on", *a)

	/*  good
	if err := run(); err != nil {
			panic(err)
	}
	*/
	/*
	   bad
	*/
	r := chi.NewRouter()
	r.Use(TimerTrace)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// или
	// r.Use(middleware.RealIP, middleware.Logger, middleware.Recoverer)

	r.Route("/sex", func(r chi.Router) {
		r.Get("/", webhook)
		r.Route("/{pistols}", func(r chi.Router) {
			r.Get("/", webhook)      // GET /cars/renault
			r.Get("/{hoy}", webhook) // GET /cars/renault/duster
		})
	})
	r.Post("/", webhook)

	fmt.Println("Hello Alise, Go main function!")
	log.Fatal(http.ListenAndServe(flagRunAddr, r))
}

func TimerTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// перед началом выполнения функции сохраняем текущее время
		start := time.Now()
		// вызываем следующий обработчик
		next.ServeHTTP(w, r)
		// после завершения замеряем время выполнения запроса
		duration := time.Since(start)
		// сохраняем или сразу обрабатываем полученный результат
		fmt.Println("TimerTrace... ", duration)
	})
}

// функция run будет полезна при инициализации зависимостей сервера перед запуском
func run() error {
	return http.ListenAndServe(`:8080`, http.HandlerFunc(webhook))
}

// функция webhook —
func webhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// разрешаем только POST-запросы
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	contentType := r.Header.Get("Content-Type")

	switch {
	case strings.Contains(contentType, "application/json"):
		// Обработка JSON
		var data map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()
		fmt.Println("JSON: ", data)

	case strings.Contains(contentType, "application/x-www-form-urlencoded"):
		// Обработка form-urlencoded
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
		fmt.Println("Form: ", r.Form)

	default:
		// Обычный текст или raw данные
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()
		fmt.Println("Raw body: ", string(body))
	}

	// установим правильный заголовок для типа данных
	w.Header().Set("Content-Type", "application/json")
	// пока установим ответ-заглушку, без проверки ошибок
	_, _ = w.Write([]byte(`
      {
        "response": {
          "text": "Извините, я пока ничего не умею"
        },
        "version": "1.0"
      }
    `))
}
