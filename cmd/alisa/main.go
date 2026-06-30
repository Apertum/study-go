package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/writer"

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
	logrus.SetLevel(logrus.TraceLevel)

	// установим форматирование логов в джейсоне &logrus.JSONFormatter{}, или просто в тексте
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	filePath := "logs/apim.log"
	// 1. Создаем папки, если их нет
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		logrus.Fatal(err)
	}

	// установим вывод логов в файл
	file, err := os.OpenFile("logs/apim.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		// Добавляем хук, который дублирует логи в файл
		logrus.AddHook(&writer.Hook{
			Writer: file,
			LogLevels: []logrus.Level{
				logrus.PanicLevel,
				logrus.FatalLevel,
				logrus.ErrorLevel,
				logrus.WarnLevel,
				logrus.InfoLevel,
				//logrus.DebugLevel,
				//logrus.TraceLevel,
			},
			// Устанавливаем JSON формат только для хука (файла)
			//Formatter: &logrus.JSONFormatter{},
		})
		// MultiWriter это если без Hook. типа сразу в несколько мест одно и то же стримить.
		//multiWriter := io.MultiWriter(os.Stdout, file)
		//logrus.SetOutput(multiWriter)
		logrus.Info("Удалось открыть файл логов!")
	} else {
		logrus.Info("Не удалось открыть файл логов, используется стандартный stderr")
	}
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
	logo()

	// обрабатываем аргументы командной строки
	parseFlags()
	logrus.Info("1 Running server on", flagRunAddr)
	logrus.Info("2 Running server on", *a)

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
