# Shorter — HTTP-сервис сокращения ссылок

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
