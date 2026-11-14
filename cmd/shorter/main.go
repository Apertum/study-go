package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof"
	"os/signal"
	"sync"
	"syscall"
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

// shutdownTimeout — период до timeout на всю остановку, а не на каждую фазу. Он задан кодом:
// набор настраиваемых параметров остановки не растёт, чтобы порядок освобождения ресурсов
// не зависел от окружения запуска.
const shutdownTimeout = 30 * time.Second

func init() {
	config.LogsInit()
}

func main() {
	// Конфигурация обязана быть итоговой до первой строки, которая читает настройки.
	// Неразбираемое ENABLE_HTTPS или trusted_subnet завершают процесс здесь — до хранилища,
	// файла аудита и базы.
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

	// Приёмники останавливаются в порядке закрытия closers: журнал аудита, хранилище, база.
	var closers []io.Closer

	if config.AuditFile != "" {
		fileH, err := audit.NewFileHandler(config.AuditFile)
		if err != nil {
			logrus.WithError(err).Warn("Failed to create audit file handler")
		} else {
			central.Subscribe(fileH)
			closers = append(closers, fileH)
		}
	}

	if config.AuditURL != "" {
		urlH := audit.NewURLHandler(config.AuditURL)
		central.Subscribe(urlH)
	}

	closers = append(closers, store)

	// Инициализируем БД один раз
	db, err := initDB(config.DatabaseDSN)
	if err != nil {
		logrus.Error("Failed to initialize database:", err)
	} else {
		closers = append(closers, db)
	}

	// SIGTERM, SIGINT и SIGQUIT — запрос плановой остановки: ctx отменяется, и с ним живут
	// слушатели. Перехват снимает диспозицию ОС, поэтому SIGQUIT не печатает дамп горутин,
	// а повторный сигнал не обрывает дозавершение.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	defer stop()

	// Фоновые удаления не выводятся из ctx сигнала: он отменён к моменту остановки, и запрос
	// в базу ушёл бы уже отменённым. Их контекст живёт до истечения периода остановки, который
	// отменяет его в shutdown.
	lifecycle, releaseLifecycle := context.WithCancel(context.Background())
	defer releaseLifecycle()

	var deletes sync.WaitGroup

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
	r.Delete("/api/user/urls", authMiddleware(handler.DeleteURLs(store, lifecycle, &deletes)).ServeHTTP)

	// pprof — маршруты для профилирования (требуют авторизацию через AuthMiddleware)
	// Типа так же, как и другие методы и с той же кукой. Можно еще как-то на другой порт, не смотрел как, попробовать если понадобится.
	r.Handle("/debug/pprof/*", authMiddleware(handler.PprofHandler(store, central)))
	r.Get("/debug/pprof/", authMiddleware(handler.PprofHandler(store, central)).ServeHTTP)

	// Внутренние эндпоинты — подсетевой контроль вместо куки: TrustedSubnetGate отвечает 403
	// раньше обработчика, поэтому отказ по сети не выдаёт состояние базы. Пустая настройка
	// разобрана в ParseFlags как нулевой префикс, и тогда доступ закрыт для всех.
	statsReader := handler.StatsFromDB(db)

	r.Route("/api/internal", func(r chi.Router) {
		r.Use(handler.TrustedSubnetGate(config.TrustedSubnet))
		r.Get("/stats", handler.ShorterStats(statsReader))
	})

	logrus.Debug("Запуск сервера на ", config.Addr)
	logrus.Debug("Base url: ", config.BaseURL)

	plan := stopPlan{
		deletes:        &deletes,
		audit:          central,
		closers:        closers,
		timeout:        shutdownTimeout,
		releaseDeletes: releaseLifecycle,
	}

	// Ошибка сюда приходит либо от слушателя (например занятый адрес), либо от оборванной
	// остановки; в обоих случаях процесс завершается кодом 1 с причиной. Причина печатается
	// в сообщении: форматтер журнала проекта выводит только его.
	if err := serve(ctx, r, plan); err != nil {
		logrus.Fatal("Сервер завершился ошибкой: ", err)
	}

	logrus.Info("Сервер остановлен")
}

// stopPlan описывает участников остановки: фазы ждутся в порядке структуры и закрываются
// в перечислении closers.
type stopPlan struct {
	deletes *sync.WaitGroup
	audit   *audit.Central

	// releaseDeletes отменяет контекст фоновых удалений, когда период остановки истёк.
	// Может быть nil — тогда горутине удаления ограничивает срок только ожидание wg.
	releaseDeletes context.CancelFunc

	closers []io.Closer
	timeout time.Duration
}

// listener поднимает один слушатель и возвращает причину его остановки.
type listener func() error

// serve слушает настроенный адрес: по HTTPS с редиректом с HTTP либо по обычному HTTP.
//
// Режимов запуска два.
//
//	HTTPS выключен (нет -s, переменная ENABLE_HTTPS пуста и в файле нет enable_https) —
//	один слушатель на config.Addr (по умолчанию :8080), открытым текстом, сертификаты не создаются.
//
//	HTTPS включён (-s, ENABLE_HTTPS либо enable_https из файла) — два слушателя сразу: TLS-сервер на
//	config.Addr (:8080) и редиректор на config.HTTPRedirectAddr (:8081), который
//	отвечает 301 на https:// с тем же путём и query.
//
// Ошибки выпуска материалов возвращаются до прослушивания портов, чтобы неверная
// конфигурация не выглядела как рабочий сервер. Возврат из serve означает «остановка
// выполнена»: по отмене ctx отпускаются оба адреса и выполняются фазы плана.
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

		return waitStop(ctx, []*http.Server{server}, []listener{server.ListenAndServe}, plan)
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

	logrus.Info("Запуск HTTPS-сервера на ", config.Addr)
	logrus.Info("Запуск HTTP-редиректора на ", config.HTTPRedirectAddr)

	// Редирект останавливается раньше основного сервера, поэтому он первым и в списке
	// слушателей, и в списке запуска.
	return waitStop(
		ctx,
		[]*http.Server{redirect, server},
		[]listener{redirect.ListenAndServe, func() error { return server.ListenAndServeTLS("", "") }},
		plan,
	)
}

// waitStop поднимает все слушатели и ждёт первую причину остановиться: отказ одного из них
// (например занятый адрес) или отмену ctx. Затем выполняет остановку по плану. Причина от
// слушателя остаётся причиной всего запуска; штатный http.ErrServerClosed сюда не попадает —
// слушатели закрывает сама остановка.
func waitStop(ctx context.Context, servers []*http.Server, listeners []listener, plan stopPlan) error {
	stopped := make(chan error, len(listeners))

	for _, listen := range listeners {
		go func() { stopped <- listen() }()
	}

	var listenErr error

	select {
	case listenErr = <-stopped:
	case <-ctx.Done():
	}

	return errors.Join(listenErr, shutdown(servers, plan))
}

// shutdown выполняет фазы остановки за один период до timeout: отпускает слушатели, дожидается
// фоновых удалений и доставки аудита, закрывает приёмники. Всё, что не успело разобраться, —
// ошибка: по ней процесс завершается кодом 1 с указанием причины.
func shutdown(servers []*http.Server, plan stopPlan) error {
	logrus.Info("Остановка: перестаем принимать новые соединения")

	ctx, cancel := context.WithTimeout(context.Background(), plan.timeout)
	defer cancel()

	var errs []error

	// Слушатели отпускаются первыми и по очереди: начатые запросы дозавершаются в пределах
	// оставшегося времени, пустые keep-alive-соединения закрываются сразу внутри Shutdown.
	for _, s := range servers {
		if err := s.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: дозавершение запросов: %w", s.Addr, err))
		}
	}

	// Фоновые удаления пишут в хранилище и базу, поэтому их ждут до закрытия приёмников.
	if plan.deletes != nil {
		if err := waitGroup(ctx, plan.deletes); err != nil {
			errs = append(errs, fmt.Errorf("фоновые удаления: %w", err))
		}
	}

	// Срок контекста удалений истекает здесь: он не выводится из контекста сигнала, поэтому
	// оборванное периодом удаление прекращается до закрытия пула соединений.
	if plan.releaseDeletes != nil {
		plan.releaseDeletes()
	}

	// Неразобранные события аудита теряются при закрытии файла приёмника, поэтому ждутся перед ним.
	if plan.audit != nil {
		if err := plan.audit.Wait(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	// Приёмники закрываются даже с просроченным периодом: ресурсы обязаны быть освобождены.
	for _, c := range plan.closers {
		if err := c.Close(); err != nil {
			errs = append(errs, fmt.Errorf("освобождение ресурсов: %w", err))
		}
	}

	return errors.Join(errs...)
}

// waitGroup ждёт завершения группы не дольше ctx: зависшая фоновая задача не должна держать
// процесс дольше одного периода до timeout на остановку.
func waitGroup(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})

	go func() {
		defer close(done)
		wg.Wait()
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
