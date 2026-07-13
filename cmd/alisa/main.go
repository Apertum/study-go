package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"

	"study-go.ru/cho/eto/internal/config"
	alise "study-go.ru/cho/eto/internal/handler"
)

var (
	a     *string
	width *int
	thumb *bool
)

func init() {
	// используем init-функцию
	a = flag.String("a2", ":8080", "alisa port")
	width = flag.Int("width", 1024, "width of the image")
	thumb = flag.Bool("thumb", false, "create thumb")

	// установим уровень логирования
	config.LogsInit()
	logo()
}

func logo() {
	//https://manytools.org/hacker-tools/ascii-banner/
	logrus.Info(" ███   ███        ███   ███        ███   ███              ")
	logrus.Info("█     █   █      █     █   █      █     █   █             ")
	logrus.Info("█  ██ █   █ ████ █  ██ █   █ ████ █  ██ █   █             ")
	logrus.Info("█   █ █   █      █   █ █   █      █   █ █   █  █   █   █  ")
	logrus.Info(" ███   ███        ███   ███        ███   ███   █   █   █  ")
}

// функция main вызывается автоматически при запуске приложения
func main() {
	// обрабатываем аргументы командной строки
	parseFlags()
	logrus.Debug("1 Running server on ", flagRunAddr)
	logrus.Trace("2 Running server on ", *a)

	r := chi.NewRouter()
	r.Use(TimerTrace)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// или
	// r.Use(middleware.RealIP, middleware.Logger, middleware.Recoverer)

	r.Route("/sex", func(r chi.Router) {
		r.Get("/", alise.Webhook)
		r.Route("/{pistols}", func(r chi.Router) {
			r.Get("/", alise.Webhook)      // GET /cars/renault
			r.Get("/{hoy}", alise.Webhook) // GET /cars/renault/duster
		})
	})
	r.Post("/", alise.Webhook)

	logrus.Info("Hello Alise, Go main function!")
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
		logrus.Info("TimerTrace... ", duration)
	})
}
