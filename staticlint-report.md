# Результаты статического анализа

Единый отчёт анализа проекта собственным `multichecker` из `cmd/staticlint`.

- Дата прогона: **2026-10-20**
- Команда: `go run ./cmd/staticlint ./...`
- Область: все пакеты репозитория, включая тестовые файлы
- Замечаний: **56**, файлов с замечаниями: **21**
- Из них с автоисправлением (`-fix`): **44**
- Анализаторов зарегистрировано: **244** (сумма групп ниже сверена с этой строкой вывода)

Набор проверок зависит от версий модулей:

| Модуль | Версия |
| --- | --- |
| `golang.org/x/tools` | v0.51.0 |
| `honnef.co/go/tools` | v0.8.2 |
| `github.com/gostaticanalysis/nilerr` | v0.1.2 |
| `github.com/gostaticanalysis/forcetypeassert` | v0.2.0 |
| `github.com/timakin/bodyclose` | v0.0.0-20260923065923-98ded9ab4ef2 |

Обычный вывод `go run ./cmd/staticlint ./...` идёт в stderr и не содержит имени
анализатора — имена и подсказки исправлений берёт `-json` (см. последний раздел).
Каталог всех проверок с формулировками: `go doc ./cmd/staticlint`.

## Сводка по анализаторам

| Анализатор | Группа | Замечаний | Автоисправление |
| --- | --- | ---: | :---: |
| `any` | x/tools · passes/modernize | 17 | да |
| `fieldalignment` | x/tools · passes | 11 | да |
| `rangeint` | x/tools · passes/modernize | 7 | да |
| `ST1000` | staticcheck · stylecheck | 5 | нет |
| `QF1011` | staticcheck · quickfix | 4 | да |
| `ST1023` | staticcheck · stylecheck | 4 | да |
| `ST1003` | staticcheck · stylecheck | 3 | нет |
| `S1009` | staticcheck · simple | 2 | нет |
| `ST1005` | staticcheck · stylecheck | 1 | нет |
| `ST1020` | staticcheck · stylecheck | 1 | нет |
| `minmax` | x/tools · passes/modernize | 1 | да |
| **итого** | | **56** | **44** |

Проверки `QF1011` и `ST1023` дублируют друг друга (4 мест): это один и тот же недостаток, 
описанный двумя классами; каждое замечание считается отдельно для своей проверки.

## Сводка по файлам

| Файл | Замечаний |
| --- | ---: |
| `cmd/db/main.go` | 9 |
| `internal/audit/audit_test.go` | 9 |
| `internal/store/mock/store.go` | 8 |
| `internal/audit/audit.go` | 3 |
| `internal/handler/shorter_test.go` | 3 |
| `internal/storage/storage.go` | 3 |
| `mocks/mock_store.go` | 3 |
| `cmd/resty/resty.go` | 2 |
| `internal/handler/bench_test.go` | 2 |
| `internal/model/models.go` | 2 |
| `internal/store/store.go` | 2 |
| `cmd/alisa/main_test.go` | 1 |
| `cmd/hw/main_test.go` | 1 |
| `cmd/shortener/main.go` | 1 |
| `internal/handler/alisa.go` | 1 |
| `internal/handler/alisa_test.go` | 1 |
| `internal/handler/shorter.go` | 1 |
| `internal/middleware/example_test.go` | 1 |
| `pkg/persistent/persistent.go` | 1 |
| `pkg/store/store.go` | 1 |
| `pkg/sum/sum.go` | 1 |

## Детали по анализаторам

Позиции указаны относительно корня репозитория, формат — `строка:колонка`.

### any (17)

Группа: x/tools · passes/modernize.

Назначение: заменить interface{} на алиас any.

Правится автоматически: `go run ./cmd/staticlint -fix <пакет>`.

`cmd/db/main.go`
- 102:30 — interface{} can be replaced by any

`cmd/resty/resty.go`
- 61:22 — interface{} can be replaced by any

`internal/audit/audit_test.go`
- 40:24 — interface{} can be replaced by any
- 75:24 — interface{} can be replaced by any
- 103:21 — interface{} can be replaced by any

`internal/middleware/example_test.go`
- 111:24 — interface{} can be replaced by any

`internal/store/mock/store.go`
- 48:69 — interface{} can be replaced by any
- 63:60 — interface{} can be replaced by any
- 78:66 — interface{} can be replaced by any
- 92:70 — interface{} can be replaced by any
- 100:15 — interface{} can be replaced by any
- 110:58 — interface{} can be replaced by any
- 110:83 — interface{} can be replaced by any
- 112:22 — interface{} can be replaced by any

`mocks/mock_store.go`
- 45:45 — interface{} can be replaced by any
- 60:42 — interface{} can be replaced by any
- 74:49 — interface{} can be replaced by any

### fieldalignment (11)

Группа: x/tools · passes.

Назначение: struct, которые займут меньше памяти при ином порядке полей.

Правится автоматически: `go run ./cmd/staticlint -fix <пакет>`.

`cmd/alisa/main_test.go`
- 61:17 — struct has 64 leading bytes of pointer data but optimal value is 56

`cmd/db/main.go`
- 137:12 — Video has 88 leading bytes of pointer data but optimal value is 64

`cmd/hw/main_test.go`
- 14:12 — want has 32 leading bytes of pointer data but optimal value is 24

`cmd/resty/resty.go`
- 13:11 — User has 32 leading bytes of pointer data but optimal value is 24

`internal/audit/audit.go`
- 61:12 — Event has 48 leading bytes of pointer data but optimal value is 40
- 77:23 — struct has 24 leading bytes of pointer data but optimal value is 16
- 97:18 — FileHandler has 16 leading bytes of pointer data but optimal value is 8

`internal/handler/alisa_test.go`
- 49:17 — struct has 128 leading bytes of pointer data but optimal value is 104

`internal/model/models.go`
- 32:14 — Session has 32 leading bytes of pointer data but optimal value is 24

`internal/storage/storage.go`
- 48:14 — Storage has 72 leading bytes of pointer data but optimal value is 40

`internal/store/store.go`
- 26:14 — Message has 56 leading bytes of pointer data but optimal value is 48

### rangeint (7)

Группа: x/tools · passes/modernize.

Назначение: заменить трёхчастный for на for-range по целому числу.

Правится автоматически: `go run ./cmd/staticlint -fix <пакет>`.

`internal/audit/audit_test.go`
- 172:6 — for loop can be modernized using range over int
- 229:6 — for loop can be modernized using range over int
- 376:6 — for loop can be modernized using range over int
- 514:6 — for loop can be modernized using range over int

`internal/handler/bench_test.go`
- 64:6 — for loop can be modernized using range over int
- 91:6 — for loop can be modernized using range over int

`internal/handler/shorter_test.go`
- 273:6 — for loop can be modernized using range over int

### ST1000 (5)

Группа: staticcheck · stylecheck.

Назначение: отсутствующий или неправильно оформленный package comment.

`internal/model/models.go`
- 1:1 — at least one file in a package should have a package comment

`internal/store/store.go`
- 1:1 — at least one file in a package should have a package comment

`pkg/persistent/persistent.go`
- 1:1 — at least one file in a package should have a package comment

`pkg/store/store.go`
- 3:1 — at least one file in a package should have a package comment

`pkg/sum/sum.go`
- 1:1 — at least one file in a package should have a package comment

### QF1011 (4)

Группа: staticcheck · quickfix.

Назначение: опустить избыточный тип в объявлении переменной.

Правится автоматически: `go run ./cmd/staticlint -fix <пакет>`.

`cmd/db/main.go`
- 25:13 — could omit type string from declaration; it will be inferred from the right-hand side
- 66:13 — could omit type string from declaration; it will be inferred from the right-hand side

`internal/audit/audit_test.go`
- 426:8 — could omit type *Central from declaration; it will be inferred from the right-hand side

`internal/handler/shorter_test.go`
- 124:11 — could omit type string from declaration; it will be inferred from the right-hand side

### ST1023 (4)

Группа: staticcheck · stylecheck.

Назначение: избыточный тип в объявлении переменной.

Правится автоматически: `go run ./cmd/staticlint -fix <пакет>`.

`cmd/db/main.go`
- 25:13 — should omit type string from declaration; it will be inferred from the right-hand side
- 66:13 — should omit type string from declaration; it will be inferred from the right-hand side

`internal/audit/audit_test.go`
- 426:8 — should omit type *Central from declaration; it will be inferred from the right-hand side

`internal/handler/shorter_test.go`
- 124:11 — should omit type string from declaration; it will be inferred from the right-hand side

### ST1003 (3)

Группа: staticcheck · stylecheck.

Назначение: неудачно выбран идентификатор (Id вместо ID, selectUrl вместо selectURL).

`cmd/db/main.go`
- 138:2 — struct field Id should be ID
- 188:3 — const Id should be ID

`internal/storage/storage.go`
- 281:6 — func selectUrl should be selectURL

### S1009 (2)

Группа: staticcheck · simple.

Назначение: избыточная проверка на nil там, где len уже определён для nil.

`internal/handler/shorter.go`
- 553:6 — should omit nil check; len() for nil slices is defined as zero

`internal/storage/storage.go`
- 312:5 — should omit nil check; len() for nil slices is defined as zero

### ST1005 (1)

Группа: staticcheck · stylecheck.

Назначение: неправильно оформленная строка ошибки.

`cmd/shortener/main.go`
- 35:10 — error strings should not be capitalized

### ST1020 (1)

Группа: staticcheck · stylecheck.

Назначение: комментарий к экспортируемой функции должен начинаться с её имени.

`internal/handler/alisa.go`
- 20:1 — comment on exported function NewApp should be of the form "NewApp ..."

### minmax (1)

Группа: x/tools · passes/modernize.

Назначение: заменить if/else встроенными min/max.

Правится автоматически: `go run ./cmd/staticlint -fix <пакет>`.

`cmd/db/main.go`
- 63:7 — if statement can be modernized using min

## Как получить отчёт заново

```bash
# текст в stderr, без имён анализаторов
go run ./cmd/staticlint ./...

# те же замечания с именами анализаторов и suggested fixes
go run ./cmd/staticlint -json ./... > /tmp/lint.json
```

Код возврата: `0` — замечаний нет, `1` — ошибка загрузки пакетов или сборки анализатора,
`3` — есть хотя бы одно замечание; с `-json` код возврата всегда `0`.

Отключить проверку на время разбора: `go run ./cmd/staticlint -ST1000=false ./...`;
отключить постоянно — убрать её из срезов в `cmd/staticlint/main.go`.

Замечаний собственного анализатора `noexit` нет: прямой os.Exit остался только в точках
входа — func main пакета main и TestMain.

Остальные проверки этого прогона касаются оформления, стиля и компоновки памяти и не
влияют на корректность кода. Проверки, которые находили другие реальные дефекты
(`copylocks`, `SA5001`, `SA4006`, `SA1019`, `forcetypeassert`, `unusedwrite`), тоже
молчат — те замечания исправлены.
