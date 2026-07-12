package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"

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

	// установим форматирование логов для консоли: 2026-07-12T12:00:00Z [info] "msg"
	logrus.SetFormatter(&consoleFormatter{})

	logFile := "logs/apim.log"
	// 1. Создаем папку для логов, если её нет
	if err := filepath.Walk(filepath.Dir("./logs"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(path, 0755)
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Не удалось создать директорию для логов")
	}

	// 2. Настраиваем lumberjack для ротации логов (лимит 10MB на файл)
	logger := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    10, // максимум 10 МБ на файл
		MaxAge:     7, // хранить до 7 старых файлов
		Compress:   true, // сжимать старые файлы
		LocalTime:  true, // использовать локальное время в именах
	}
	if err := logger.Close(); err != nil {
		logrus.WithError(err).Warn("Ошибка при инициализации lumberjack")
	}

	// 3. Добавляем хук, который дублирует логи в файл с ротацией в JSON формате
	logrus.AddHook(&jsonFileHook{
		Writer: logger,
		LogLevels: []logrus.Level{
			logrus.PanicLevel,
			logrus.FatalLevel,
			logrus.ErrorLevel,
			logrus.WarnLevel,
			logrus.InfoLevel,
			//logrus.DebugLevel,
			//logrus.TraceLevel,
		},
	})
	logrus.Info("Удалось настроить файл логов с ротацией (10MB limit)!")
}

// consoleFormatter — форматер для консоли: 2026-07-12T12:00:00Z [info] "msg"
type consoleFormatter struct{}

func (f *consoleFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	level := entry.Level
	levelStr := level.String()
	if levelStr == "" {
		levelStr = "info"
	}
	msg := entry.Message
	timestamp := entry.Time.Format("02.01_15:04:05.000")
	return []byte(fmt.Sprintf("%s [%s] \"%s\"\n", timestamp, string(unicode.ToUpper([]rune(levelStr)[0])), msg)), nil
}

// jsonFileHook — хук для logrus, который пишет логи в файл в JSON формате
type jsonFileHook struct {
	Writer    io.Writer
	LogLevels []logrus.Level
}

func (h *jsonFileHook) Levels() []logrus.Level {
	return h.LogLevels
}

func (h *jsonFileHook) Fire(entry *logrus.Entry) error {
	formatter := &logrus.JSONFormatter{}
	bytes, err := formatter.Format(entry)
	if err != nil {
		return err
	}
	_, err = h.Writer.Write(bytes)
	return err
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
	logrus.Debug("1 Running server on", flagRunAddr)
	logrus.Trace("2 Running server on", *a)

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
