package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof"
	"sync"
	"time"

	"study-go.ru/cho/eto/internal/audit"
	config "study-go.ru/cho/eto/internal/config"
	"study-go.ru/cho/eto/internal/handler"
	internalMiddleware "study-go.ru/cho/eto/internal/middleware"
	"study-go.ru/cho/eto/internal/secure"
	"study-go.ru/cho/eto/internal/storage"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
)

// listenTimeout ограничивает ожидание заголовков запроса на обоих слушателях.
const listenTimeout = 5 * time.Second

func init() {
	config.LogsInit()
}

func main() {
	// Конфигурация обязана быть итоговой до первой строки, которая читает настройки.
	// Неразбираемое ENABLE_HTTPS завершает процесс здесь — до хранилища, файла аудита и базы.
	if err := config.ParseFlags(); err != nil {
		logrus.Fatal(err)
	}

	logrus.Info(`
╔═══════════════════════════════╗
║        short-short F          ║
╚═══════════════════════════════╝
    `)

	// загружаем данные из файла (если существует)
	store := storage.New(config.FileName)

	// Аудит-центр: создаём и подписываем наблюдателей по конфигурции
	central := audit.NewCentral()

	if config.AuditFile != "" {
		fileH, err := audit.NewFileHandler(config.AuditFile)
		if err != nil {
			logrus.WithError(err).Warn("Failed to create audit file handler")
		} else {
			central.Subscribe(fileH)
			defer fileH.Close()
		}
	}

	if config.AuditURL != "" {
		urlH := audit.NewURLHandler(config.AuditURL)
		central.Subscribe(urlH)
	}

	// Инициализируем БД один раз
	db, err := initDB(config.DatabaseDSN)
	if err != nil {
		logrus.Error("Failed to initialize database:", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	defer cancel()

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
	r.Handle("/", authMiddleware(handler.ShorterPost(store, central)))
	r.Handle("/api/shorten", authMiddleware(handler.ShorterPost(store, central)))
	r.Handle("/api/shorten/batch", authMiddleware(handler.ShorterBatchPost(store, central)))
	r.Handle("/api/user/urls", authMiddleware(handler.ShorterUserURLsGet(db)))
	r.Get("/{id}", handler.ShorterGet(store, central))
	r.Delete("/api/user/urls", authMiddleware(handler.DeleteURLs(store, ctx, &wg)).ServeHTTP)

	// pprof — маршруты для профилирования (требуют авторизацию через AuthMiddleware)
	// Типа так же, как и другие методы и с той же кукой. Можно еще как-то на другой порт, не смотрел как, попробовать если понадобится.
	r.Handle("/debug/pprof/*", authMiddleware(handler.PprofHandler(store, central)))
	r.Get("/debug/pprof/", authMiddleware(handler.PprofHandler(store, central)).ServeHTTP)

	logrus.Debug("Запуск сервера на ", config.Addr)
	logrus.Debug("Base url: ", config.BaseURL)

	if err := serve(ctx, r, stopPlan{timeout: 30 * time.Second}); err != nil {
		logrus.Fatal(err)
	}
}

// stopPlan описывает фазы остановки сервера.
type stopPlan struct {
	timeout time.Duration
	closers []io.Closer
	deletes *sync.WaitGroup
	audit   interface{ Wait(context.Context) error }
}

// serve слушает настроенный адрес: по HTTPS с редиректом с HTTP либо по обычному HTTP.
//
// Режимов запуска два.
//
//	HTTPS выключен (нет -s, переменная ENABLE_HTTPS пуста и в файле нет enable_https) —
//	один слушатель на config.Addr (по умолчанию :8080), открытым текстом, сертификаты не создаются.
//
//	HTTPS включён (-s, ENABLE_HTTPS л��бо enable_https из файла) — два слушателя сразу: TLS-сервер на
//	config.Addr (:8080) и редиректор на config.HTTPRedirectAddr (:8081), который
//	отвечает 301 на https:// с тем же путём и query.
//
// Ошибки выпуска материалов возвращаются до прослушивания портов, чтобы неверная
// конфигурация не выглядела как рабочий сервер.
func serve(ctx context.Context, handler http.Handler, plan stopPlan) error {
	// Основной сервер занимает -a / SERVER_ADDRESS. TLSConfig пока пустая — при
	// включённом HTTPS сертификат подставится ниже.
	server := &http.Server{
		Addr:              config.Addr,
		Handler:           handler,
		ReadHeaderTimeout: listenTimeout,
	}

	// Ветвь «как раньше»: единственный порт и обычный HTTP.
	if !config.EnableHTTPS {
		logrus.Info("Запуск HTTP-сервера на ", config.Addr)

		go func() {
			<-ctx.Done()
			_ = server.Shutdown(ctx)
		}()

		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	}

	// Дальше только HTTPS. Адреса сверяются до bind: иначе второй слушатель падал бы с
	// невнятным «bind: address already in use» уже внутри горутины.
	err := secure.AddrConflict(config.Addr, config.HTTPRedirectAddr)
	if err != nil {
		return err
	}

	// Материал TLS берётся из -cert-file/-key-file (cert.pem/key.pem): готовая пара
	// переиспользуется байт в байт, битая или истёкшая перевыпускается на месте.
	material, err := secure.LoadOrGenerate(config.CertFile, config.KeyFile)
	if err != nil {
		return fmt.Errorf("материалы TLS не готовы: %w", err)
	}

	// Сертификат кладём прямо в конфигурацию сервера: пути файлов в ListenAndServeTLS
	// ниже не передаются.
	server.TLSConfig = material.ServerTLS()

	// Второй слушатель ничего не обслуживает сам — только редирект на основной адрес.
	redirect := &http.Server{
		Addr:              config.HTTPRedirectAddr,
		Handler:           secure.RedirectHandler(config.Addr),
		ReadHeaderTimeout: listenTimeout,
	}

	// Редиректор поднимается в горутине: за ней ListenAndServeTLS уже не возвращает
	// управление, а отвечать на обоих портах нужно одновременно.
	go func() {
		logrus.Info("Запуск HTTP-редиректора на ", config.HTTPRedirectAddr)

		if err := redirect.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logrus.WithError(err).Error("HTTP-редиректор завершился ошибкой")
		}
	}()

	// Освобождаем порт редиректа, когда основной сервер всё же завершился.
	defer func() { _ = redirect.Close() }()

	logrus.Info("Запуск HTTPS-сервера на ", config.Addr)

	go func() {
		<-ctx.Done()
		_ = redirect.Shutdown(ctx)
		_ = server.Shutdown(ctx)
	}()

	// Эта строка блокирует до остановки процесса; ошибку печатает main одним logrus.Fatal.
	err = server.ListenAndServeTLS("", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}

	return err
}

// shutdown закрывает приёмники в порядке плана и дожидается фоновых задач.
func shutdown(servers []*http.Server, plan stopPlan) error {
	deadline := time.Now().Add(plan.timeout)

	var firstErr error

	// Дождаемся фоновых удалений.
	if plan.deletes != nil {
		plan.deletes.Wait()
	}

	// Дождемся доставки аудита с таймаутом.
	if plan.audit != nil {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		err := plan.audit.Wait(ctx)
		cancel()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	// Закроем приёмники.
	for _, c := range plan.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// Остановим серверы последовательно: каждый следующий закрывается после
	// предыдущего, чтобы порядок фаз сохранялся. Используем Shutdown чтобы
	// дождаться дозавершения запросов — висящий запрос удержит сервер до timeout.
	for _, s := range servers {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		_ = s.Shutdown(ctx)
		cancel()
		time.Sleep(10 * time.Millisecond)
	}

	if time.Now().After(deadline) && firstErr == nil {
		return context.DeadlineExceeded
	}

	return firstErr
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
