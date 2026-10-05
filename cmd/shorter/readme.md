Shorter — HTTP-сервис сокращения ссылок

Сервис сокращает длинные URL-адреса, сохраняя результаты в PostgreSQL.
Поддерживает аутентификацию пользователей через подписанные куки и привязку сокращённых URL к пользователям.

## Архитектура

```
┌──────────┐     ┌──────────────┐     ┌─────────┐
│  Клиент  │────▶│   Chi Router │────▶│ Handlers│
└──────────┘     └──────────────┘     └────┬────┘
                                            │
                              ┌─────────────┼─────────────┐
                              ▼             ▼             ▼
                        AuthMiddleware ShorterPost   ShorterGet
                              │             │             │
                              ▼             ▼             ▼
                         [401/403]      Storage       Redirect
                                            │
                                            ▼
                                      PostgreSQL
                                     (url_srv, usr)
```

### Компоненты

| Компонент            | Назначение                                         |
|----------------------|----------------------------------------------------|
| `chi.Router`         | Маршрутизация HTTP-запросов, глобальные middleware |
| `AuthMiddleware`     | Проверка HMAC-подписанной куки `user_id`           |
| `ShorterPost`        | POST / — сокращение одной ссылки                   |
| `ShorterBatchPost`   | POST /api/shorten/batch — пакетное сокращение      |
| `ShorterGet`         | GET /{id} — редирект на оригинальный URL           |
| `ShorterPing`        | GET /ping — проверка доступности БД                |
| `ShorterLoginPost`   | POST /login — вход пользователя, выдача куки       |
| `ShorterUserURLsGet` | GET /api/user/urls — список ссылок пользователя    |
| `Storage`            | In-memory кэш + синхронизация с PostgreSQL         |
| `GzipMiddleware`     | Сжатие ответов gzip                                |

### База данных

Таблицы:

- **`usr`** — пользователи (`id SERIAL`, `usr_name TEXT UNIQUE`, `paswd TEXT`)
- **`url_srv`** — сокращённые ссылки (`id`, `uuid`, `original_url`, `short_url`, `usr_id → usr(id)`)

---

## Аудит запросов (паттерн «Наблюдатель»)

После успешной обработки любого запроса к хэндлерам `POST /`, `POST /api/shorten` или `GET /{id}` формируется событие аудита и отправляется всем подключённым приёмникам.

### Архитектура

```
                  ┌───────────────┐
  хэндлеры ────▶  │   Central     │  реестр наблюдателей
                  │  Notify(event)│
                  └──────┬────────┘
                         │
               Subscribe │  Subscribe
                    ┌─────┴─────┐
                    ▼           ▼
           FileHandler     URLHandler
       (запись в файл)   (POST на сервер)
```

### Компоненты

| Компонент         | Роль                                                       |
|-------------------|------------------------------------------------------------|
| `Event`           | Структура события: `ts`, `action`, `user_id`, `url`        |
| `EventHandler`    | Интерфейс наблюдателя с методом `Handle(Event)`             |
| `Central`         | Реестр: хранит список наблюдателей, `Subscribe` / `Notify`  |
| `FileHandler`     | Дописывает JSON-строку в файл (append mode)                |
| `URLHandler`      | Отправляет событие POST-запросом на удалённый сервер        |

### Принцип работы

1. **Подписка** — при старте сервера создаётся один `Central`. Если переданы параметры `--audit-file` или `--audit-url`, соответствующие наблюдатели подписываются через `Subscribe()`.
2. **Уведомление** — после успешной обработки хэндлер вызывает `central.Notify(event)`.
3. **Рассылка** — `Notify` проходит по списку наблюдателей и запускает каждого в горутине, не блокируя хэндлер.
4. **Добавление нового приёмника** — реализуй интерфейс `EventHandler` и подпиши через `Subscribe()`. Хэндлеры и `Central` не меняются.

Схема рассылки при двух приёмниках:

```
POST /api/shorten успешно обработан
  └── Notify(Event{ts:1700, action:"shorten", user_id:"1", url:"https://..."})
        ├──► FileHandler.Handle()     → дописывает строку в audit.log
        └──► URLHandler.Handle()      → POST https://monitor.internal/audit
```

### Формат события

```json
{
  "ts": 1700000000,
  "action": "shorten",
  "user_id": "12315134",
  "url": "https://mylongdomain.com/long/path"
}
```

- `ts` — unix timestamp события
- `action` — `shorten` (создание) или `follow` (переход по ссылке)
- `user_id` — идентификатор пользователя (отсутствует, если запрос без авторизации)
- `url` — оригинальный (не сокращённый) URL

### Настройка параметров

| Флаг / Переменная     | Описание                                 | По умолчанию |
|-----------------------|------------------------------------------|--------------|
| `--audit-file` / `AUDIT_FILE` | Путь к файлу для логов аудита           | — (выключено)|
| `--audit-url` / `AUDIT_URL`   | URL удалённого сервера-приёмника аудита | — (выключено)|

Если параметр не передан, соответствующий приёмник не создаётся.

### Пример запуска с аудитом

```bash
go run cmd/shorter/main.go \
  -d "postgres://..." \
  -k "secret" \
  --audit-file /var/log/shortener_audit.jsonl \
  --audit-url "https://monitor.internal/api/audit"
```

События будут дублироваться одновременно в файл и на удалённый сервер.

---

## API

### POST `/ping`

Проверка доступности PostgreSQL.

**Ответ:** `200 OK`, тело: `OK`

---

### POST `/login`

Вход пользователя. Если пользователь с таким `usr_name` не существует — создаётся новый.

**Запрос:**

```json
{
  "usr_name": "alice"
}
```

**Ответ:** `200 OK`

```json
{
  "user_id": "42"
}
```

+ кука `user_id=42:<hmac_sha256_signature>`

**Сценарии проверки куки:**

| Сценарий                           | Код |
|------------------------------------|-----|
| Нет куки `user_id`                 | 401 |
| Кука есть, подпись невалидна       | 401 |
| Подпись верна, пользователь удалён | 403 |
| Всё ок                             | 200 |

---

### POST `/` или `POST /api/shorten`

Сокращение одной ссылки. Требует авторизацию.

**Запрос:**

```json
{
  "url": "https://example.com/very/long/path"
}
```

Или raw-тело (Content-Type: text/plain).

**Ответ:** `201 Created`

```json
{
  "uuid": "0",
  "short_url": "http://short.ru/a1b2c3d4",
  "original_url": "https://example.com/very/long/path"
}
```

**Если URL уже существует:** `409 Conflict`

```json
{
  "short_url": "http://short.ru/a1b2c3d4",
  "original_url": "https://example.com/very/long/path"
}
```

---

### POST `/api/shorten/batch`

Пакетное сокращение. Требует авторизацию.

**Запрос:**

```json
[
  {
    "correlation_id": "req-1",
    "url": "https://example.com/one"
  },
  {
    "correlation_id": "req-2",
    "url": "https://example.com/two"
  }
]
```

**Ответ:** `201 Created`

```json
[
  {
    "correlation_id": "req-1",
    "uuid": "0",
    "short_url": "http://short.ru/a1b2c3d4",
    "original_url": "https://example.com/one"
  }
]
```

---

### GET `/{id}`

Редирект на оригинальный URL. Не требует авторизации.

**Ответ:** `307 Temporary Redirect`, заголовок `Location: <original_url>`

---

### GET `/api/user/urls`

Список всех ссылок, сокращённых текущим пользователем. Требует авторизацию.

**Ответ:** `200 OK`

```json
[
  {
    "short_url": "http://short.ru/a1b2c3d4",
    "original_url": "https://example.com/one"
  }
]
```

Если ссылок нет: `204 No Content`

---

## Запуск

### Переменные окружения

| Переменная          | Описание                            | По умолчанию       |
|---------------------|-------------------------------------|--------------------|
| `SERVER_ADDRESS`    | Адрес сервера (переопределяет `-a`) | `:8080`            |
| `BASE_URL`          | Префикс коротких URL                | `http://short.ru/` |
| `FILE_STORAGE_PATH` | Путь к файлу (fallback)             | `data.json`        |
| `DATABASE_DSN`      | DSN PostgreSQL                      | —                  |
| `COOKIE_KEY`        | Секретный ключ HMAC-подписи куки    | —                  |

### Флаги командной строки

| Флаг | Описание                    | По умолчанию       |
|------|-----------------------------|--------------------|
| `-a` | Адрес сервера               | `:8080`            |
| `-b` | Базовый URL коротких ссылок | `http://short.ru/` |
| `-f` | Файл хранения (fallback)    | `data.json`        |
| `-d` | DSN PostgreSQL              | —                  |
| `-k` | Ключ HMAC-подписи куки      | —                  |

### Примеры запуска

**Минимальный (file storage):**

```bash
go run cmd/shorter/main.go
```

**С PostgreSQL:**

```bash
DATABASE_DSN="postgres://user:pass@localhost:5432/study?sslmode=disable" \
COOKIE_KEY="my-secret-key-for-hmac" \
go run cmd/shorter/main.go
```

**С флагами:**

```bash
go run cmd/shorter/main.go \
  -a ":9090" \
  -b "http://s.local/" \
  -d "postgres://..." \
  -k "secret"
```

---

## Полный сценарий использования

### 1. Вход (получаем куку)

```bash
curl -v -X POST http://localhost:8080/login \
  -H "Content-Type: application/json" \
  -d '{"usr_name":"alice"}'
```

Сервер ответит `200` с `"user_id": "1"` и установит куку `user_id=1:<signature>`.

### 2. Сокращение ссылки (нужна кука)

```bash
curl -v -X POST http://localhost:8080/api/shorten \
  -b "user_id=1:<signature>" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/very/long/path"}'
```

Без куки → `401 Unauthorized`.

### 3. Переход по короткой ссылке

```bash
curl -v http://localhost:8080/a1b2c3d4
```

→ `307 Temporary Redirect` на оригинальный URL.

### 4. Список ссылок пользователя

```bash
curl -v http://localhost:8080/api/user/urls -b "user_id=1:<signature>"
```

→ `200 OK` с JSON-массивом ссылок.
→ `204 No Content`, если ссылок нет.

---

## Безопасность

//доработка fix1
- `AuthMiddleware` защищает критичные маршруты: POST /, POST /api/shorten, POST /api/shorten/batch, GET /api/user/urls, DELETE /api/user/urls.
- `/debug/pprof/*` — маршруты профилирования (pprof) требуют авторизованную куку (`user_id`). Без неё — 401 Unauthorized.
- `GET /{id}` не требует авторизации — это публичный редирект.

---

## Потокобезопасность

- `Storage` использует `sync.Mutex` для защиты in-memory кэша.
- `Storage.fileMu` — отдельная мьютекс для атомарной записи файла.
- PostgreSQL запросы выполняются через `db.QueryRowContext` / `db.QueryContext` с таймаутами.

## Структура проекта

```
cmd/shorter/
  main.go          # Точка входа, роутинг, инициализация
internal/
  handler/shorter.go    # HTTP-хендлеры + AuthMiddleware
  storage/storage.go    # In-memory + PostgreSQL storage
  config/flags.go       # Флаги и env-переменные
  config/logs.go        # Инициализация логирования
  middleware/gzip.go    # Gzip-сжатие ответов
migrations/             # SQL-миграции БД
```

---
//доработка fix1 в доку написал, а вот как типа в файл чтоб писалось не понял как сделать. просто скопипаслил из cmd
## Бенчмарки и профилирование

### Бенчмарки

В `internal/handler/bench_test.go` определены 6 бенчмарков для оценки производительности хендлеров и middleware:

| Бенчмарк                             | Время        | Память     | Allocs/op |
|--------------------------------------|-------------|------------|-----------|
| `BenchmarkShorterGet_not_found`      | ~6200 ns/op | ~6630 B/op | 33        |
| `BenchmarkShorterPost_single`        | ~3800 ns/op | ~7390 B/op | 31        |
| `BenchmarkShorterBatchPost_10`       | ~10500 ns/op| ~9920 B/op | 58        |
| `BenchmarkShorterBatchPost_100`      | ~68000 ns/op| ~36570 B/op| 248       |
| `BenchmarkGzipMiddleware_enabled`    | ~135000 ns/op| ~822400 B/op| 47      |
| `BenchmarkGzipMiddleware_disabled`   | ~2900 ns/op | ~6600 B/op | 25        |

**Запуск:**

```bash
# Все бенчмарки с замерами памяти и CPU-профилем
go test -bench=. -benchmem -count=1 -cpuprofile=profiles/base.pprof ./internal/handler/

# Один конкретный бенчмарк
go test -bench=BenchmarkShorterPost_single -benchmem ./internal/handler/
```

### CPU-профилирование

Профили сохраняются в `profiles/base.pprof` (baseline) и `profiles/result.pprof` (после изменений).

**Генерация профилей:**

```bash
# Базовый запуск — фиксируем baseline
go test -bench=. -benchmem -count=1 -cpuprofile=profiles/base.pprof ./internal/handler/

# После внесения изменений запускаем ещё раз
go test -bench=. -benchmem -count=1 -cpuprofile=profiles/result.pprof ./internal/handler/
```

**Анализ:**

```bash
# Топ-функции по потреблению CPU
go tool pprof -top profiles/base.pprof

# Топ-функции по накопительному времени
go tool pprof -top -cum profiles/base.pprof

# Интерактивный просмотр с graphviz
go tool pprof -http=:8080 profiles/base.pprof
```

### Сравнение профилей (base vs result)

#### Дифф бенчмарков

Сравнение времени выполнения, потребления памяти и аллокаций между двумя запусками:

| Benchmark                            | Time (base → result)   | Memory (base → result) | Allocs |
|--------------------------------------|------------------------|------------------------|--------|
| `BenchmarkShorterGet_not_found`      | 6515 → 6171 (-344)     | 6632 → 6632 (+0)       | +0     |
| `BenchmarkShorterPost_single`        | 3943 → 3681 (-262)     | 7389 → 7389 (+0)       | +0     |
| `BenchmarkShorterBatchPost_10`       | 10924 → 10198 (-726)   | 9918 → 9918 (+0)       | +0     |
| `BenchmarkShorterBatchPost_100`      | 67866 → 67546 (-320)   | 36572 → 36572 (+0)     | +0     |

Все бенчмарки показали небольшое улучшение, что подтверждает отсутствие регрессий после внесённых изменений. Различия в пределах погрешности системы.

#### Дифф CPU-профилей

Сравнение распределения CPU-нагрузок между запусками:

```diff
- Duration: 8.15s, Total samples = 13630ms (167.34%)
+ Duration: 8.71s, Total samples = 13950ms (160.14%)
-    1130ms  8.29%  8.29%     1130ms  8.29%  runtime.futex
+    1430ms 10.25% 10.25%     1430ms 10.25%  runtime.futex
-     790ms  5.80% 14.09%      790ms  5.80%  runtime.memclrNoHeapPointers
+     550ms  3.94% 14.19%      550ms  3.94%  runtime.memclrNoHeapPointers
-     500ms  3.67% 17.75%      920ms  6.75%  encoding/json.checkValid
+     530ms  3.80% 17.99%      960ms  6.88%  encoding/json.checkValid
```

Основные потребители CPU в обоих профилях одинаковые:
1. `runtime.futex` — системные вызовы синхронизации (~9-10%)
2. `runtime.memclrNoHeapPointers` — очистка памяти GC (~4-6%)
3. `encoding/json.checkValid` — парсинг JSON (~3-4%)
4. `runtime.scanObject` — сканирование объектов GC (~3-5%)

Различия незначительны (±200ms на функцию), что подтверждает стабильность производительности между запусками без изменений кода.

**Генерация сравнения:**

```bash
# Сравнить два профиля
go tool pprof -top -nodecount=5 profiles/base.pprof profiles/result.pprof
```

### Скрипт автоматизации

```bash
# Запуск базового бенчмарка с профилем
bash scripts/run_benchmarks.sh base

# Запуск после изменений (результат)
bash scripts/run_benchmarks.sh result

# Сравнение профилей после обоих запусков
go tool pprof -top -nodecount=10 profiles/base.pprof profiles/result.pprof
```
