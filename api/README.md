# api

В этой директории принято размещать proto-файлы или файлы в формате OpenAPI/Swagger для описания контракта сервиса.

## Контракт gRPC

`shortener/v1/shortener.proto` — контракт сервиса `shortener.v1.ShortenerService` с тремя unary-методами:
`ShortenURL`, `ExpandURL`, `ListUserURLs`. Сгенерированный код (`shortener.pb.go`, `shortener_grpc.pb.go`)
лежит рядом с `.proto` и является частью исходного кода: сборка и тесты не требуют ни `protoc`,
ни плагинов protobuf.

## Перегенерация

Генерация нужна только при изменении `.proto` и выполняется на машине разработки:

```bash
B="$(go env GOPATH)/bin"
protoc --proto_path=api \
  --plugin=protoc-gen-go="$B/protoc-gen-go" \
  --plugin=protoc-gen-go-grpc="$B/protoc-gen-go-grpc" \
  --go_out=api --go_opt=paths=source_relative \
  --go-grpc_out=api --go-grpc_opt=paths=source_relative \
  shortener/v1/shortener.proto
```

Каталог `$(go env GOPATH)/bin` в командах выше — место установки плагинов; если его нет в `PATH`,
плагины передаются флагами `--plugin=` с абсолютным путём, как в примере. Импорт
`google/protobuf/empty.proto` разрешается из встроенного include-каталога `protoc`.

Установить плагины:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
```

Зафиксированные версии:

| Инструмент | Версия |
| --- | --- |
| `protoc` | `libprotoc 3.21.12` |
| `protoc-gen-go` | `v1.36.12` |
| `protoc-gen-go-grpc` | `v1.6.2` |
| `google.golang.org/protobuf` в `go.mod` | `v1.36.12` |

Версии плагина и модуля `google.golang.org/protobuf` обязаны совпадать по минорной ветке, иначе
сгенерированный код расходится с рантаймом. После перегенерации изменения в `*.pb.go` коммитятся
вместе с изменением `.proto`.

## Проверки

Сгенерированный код исключён из проверки собственным линтером: `cmd/staticlint` сообщает про
`interface{}` вместо `any`, отсутствие пакетного комментария и непроверенные утверждения типов
в `*.pb.go`, а править файл с пометкой `DO NOT EDIT` нельзя. Команда проверки проекта без каталога
контрактов:

```bash
go list ./... | grep -v '/api/' | xargs go run ./cmd/staticlint
```

`gofmt -l .` и `go vet ./...` каталог не исключают: сгенерированный код проходит их как есть.

## Вызов

Сервер принимает gRPC на адресе `-grpc-addr` / `GRPC_ADDRESS` / `grpc_address` (по умолчанию
`:8082`) по открытому тексту: TLS не используется независимо от `ENABLE_HTTPS` — включённый HTTPS
меняет только состав HTTP-слушателей. Учётные данные передаются в metadata `authorization` и совпадают
со значением куки `user_id`: `<id>:<HMAC-SHA256 от id по COOKIE_KEY в hex>`, его выдаёт `POST /login`
заголовком `Set-Cookie`. `ExpandURL` публичен, как `GET /{id}`, и учётных данных не требует.

Reflection на сервере не зарегистрирован, поэтому схема берётся из `.proto`:

```bash
# Переход по ссылке — без учётных данных.
grpcurl -plaintext -import-path api -proto shortener/v1/shortener.proto \
  -d '{"id":"abc"}' localhost:8082 shortener.v1.ShortenerService/ExpandURL

# Сокращение — значение metadata то же, что у куки user_id.
grpcurl -plaintext -import-path api -proto shortener/v1/shortener.proto \
  -H 'authorization: 1:c8e0b3a4d97f0e5142ab6cd3f1a20b7d9e5c4a13f6b28d0e7a94c1b5d2e8f036' \
  -d '{"url":"https://example.com/page"}' localhost:8082 shortener.v1.ShortenerService/ShortenURL

# Список ссылок пользователя; пустой список — успешный ответ без элементов.
grpcurl -plaintext -import-path api -proto shortener/v1/shortener.proto \
  -H 'authorization: 1:c8e0b3a4d97f0e5142ab6cd3f1a20b7d9e5c4a13f6b28d0e7a94c1b5d2e8f036' \
  localhost:8082 shortener.v1.ShortenerService/ListUserURLs
```

Подпись в примерах — образец формата, реальное значение зависит от `COOKIE_KEY`. Причина отказа
различается кодом: `UNAUTHENTICATED` при отсутствии или неверной подписи, `PERMISSION_DENIED`
при неизвестном пользователе, `INTERNAL` при ошибке хранилища.
