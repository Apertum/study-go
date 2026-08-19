package main

import (
	"flag"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"

	"study-go.ru/cho/eto/internal/config"
	alise "study-go.ru/cho/eto/internal/handler"

	"compress/gzip"
	"io"
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

	run()
	logrus.Fatal("Конец всему!")
}

func run() error {

	// создаём экземпляр приложения, пока без внешней зависимости хранилища сообщений
	appInstance := alise.NewApp(nil)

	r := chi.NewRouter()
	// оборачиваем хендлер webhook в middleware с логированием и поддержкой gzip
	r.Use(gzipMiddleware)
	r.Use(TimerTrace)
	r.Use(middleware.ClientIPFromRemoteAddr)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// или
	// r.Use(middleware.RealIP, middleware.Logger, middleware.Recoverer)

	r.Route("/sex", func(r chi.Router) {
		r.Get("/", appInstance.WebhookApp)
		r.Route("/{pistols}", func(r chi.Router) {
			r.Get("/", appInstance.WebhookApp)      // GET /cars/renault
			r.Get("/{hoy}", appInstance.WebhookApp) // GET /cars/renault/duster
		})
	})
	r.Post("/", appInstance.WebhookApp)

	logrus.Info("Hello Alise, Go main function!")
	http.ListenAndServe(flagRunAddr, r)
	return nil
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

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// по умолчанию устанавливаем оригинальный http.ResponseWriter как тот,
		// который будем передавать следующей функции
		ow := w

		// проверяем, что клиент умеет получать от сервера сжатые данные в формате gzip
		acceptEncoding := r.Header.Get("Accept-Encoding")
		supportsGzip := strings.Contains(acceptEncoding, "gzip")
		if supportsGzip {
			// оборачиваем оригинальный http.ResponseWriter новым с поддержкой сжатия
			cw := newCompressWriter(w)
			// меняем оригинальный http.ResponseWriter на новый
			ow = cw
			// не забываем отправить клиенту все сжатые данные после завершения middleware
			defer cw.Close()
		}

		// проверяем, что клиент отправил серверу сжатые данные в формате gzip
		contentEncoding := r.Header.Get("Content-Encoding")
		sendsGzip := strings.Contains(contentEncoding, "gzip")
		if sendsGzip {
			// оборачиваем тело запроса в io.Reader с поддержкой декомпрессии
			cr, err := newCompressReader(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			// меняем тело запроса на новое
			r.Body = cr
			defer cr.Close()
		}

		// передаём управление хендлеру
		next.ServeHTTP(ow, r)
	})
}

// compressWriter реализует интерфейс http.ResponseWriter и позволяет прозрачно для сервера
// сжимать передаваемые данные и выставлять правильные HTTP-заголовки
type compressWriter struct {
	w  http.ResponseWriter
	zw *gzip.Writer
}

func newCompressWriter(w http.ResponseWriter) *compressWriter {
	return &compressWriter{
		w:  w,
		zw: gzip.NewWriter(w),
	}
}

func (c *compressWriter) Header() http.Header {
	return c.w.Header()
}

func (c *compressWriter) Write(p []byte) (int, error) {
	return c.zw.Write(p)
}

func (c *compressWriter) WriteHeader(statusCode int) {
	if statusCode < 300 {
		c.w.Header().Set("Content-Encoding", "gzip")
	}
	c.w.WriteHeader(statusCode)
}

// Close закрывает gzip.Writer и досылает все данные из буфера.
func (c *compressWriter) Close() error {
	return c.zw.Close()
}

// compressReader реализует интерфейс io.ReadCloser и позволяет прозрачно для сервера
// декомпрессировать получаемые от клиента данные
type compressReader struct {
	r  io.ReadCloser
	zr *gzip.Reader
}

func newCompressReader(r io.ReadCloser) (*compressReader, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}

	return &compressReader{
		r:  r,
		zr: zr,
	}, nil
}

func (c compressReader) Read(p []byte) (n int, err error) {
	return c.zr.Read(p)
}

func (c *compressReader) Close() error {
	if err := c.r.Close(); err != nil {
		return err
	}
	return c.zr.Close()
}
