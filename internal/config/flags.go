// Package config предоставляет конфигурацию сервера URL-сократителя.
//
// Переменные пакета заполняются при вызове ParseFlags() из main(). Источников три:
//   - переменные окружения: SERVER_ADDRESS, BASE_URL, FILE_STORAGE_PATH, DATABASE_DSN, COOKIE_KEY,
//     AUDIT_FILE, AUDIT_URL, ENABLE_HTTPS, CERT_FILE, KEY_FILE, HTTP_SERVER_ADDRESS;
//   - флаги командной строки (-a, -b, -f, -d, -k, -audit-file, -audit-url, -s, -cert-file,
//     -key-file, -http-addr);
//   - JSON-файл конфигурации: путь задаётся флагом -c (он же -config) либо переменной CONFIG.
//
// Порядок источников для каждой настройки: непустая переменная окружения > явно переданный флаг >
// файл > значение флага по умолчанию. Флаг считается переданным явно, только если он указан в команде,
// — даже когда его значение совпадает со значением по умолчанию. Пустая строка в переменной и в файле
// означает «источник молчит». Ошибки файла (нет файла, не читается, не разбирается) предупреждением не
// прерывают запуск: настройки тогда приходят из старших источников.
//
// Значение ENABLE_HTTPS разбирает ParseFlags(): переменная важнее флага -s и файла, а неразбираемое
// значение возвращается ошибкой, чтобы main завершился до прослушивания портов. ParseEnabled остаётся
// экспортной для клиента cmd/shortener — у него собственный набор флагов.
package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/sirupsen/logrus"
)

// EnvEnableHTTPS — имя переменной окружения, включающей HTTPS.
const EnvEnableHTTPS = "ENABLE_HTTPS"

// Экспортированные переменные конфигурации, заполняемые ParseFlags().
//
// Значение берётся из старшего непустого источника: переменная окружения, явно переданный флаг,
// JSON-файл, значение флага по умолчанию.
var (
	// BaseURL — базовый префикс для коротких URL (например, "http://short.ru/").
	// Флаг: -b | ENV: BASE_URL
	BaseURL string

	// Addr — адрес и порт HTTP-сервера (например, ":8080").
	// Флаг: -a | ENV: SERVER_ADDRESS
	Addr string

	// FileName — путь к JSON-файлу хранения (fallback без БД).
	// Флаг: -f | ENV: FILE_STORAGE_PATH
	FileName string = "dataNN.json"

	// DatabaseDSN — DSN-строка для подключения к PostgreSQL.
	// Флаг: -d | ENV: DATABASE_DSN
	DatabaseDSN string

	// CookieKey — секретный ключ для HMAC-SHA256 подписи куки user_id.
	// Флаг: -k | ENV: COOKIE_KEY
	CookieKey string

	// AuditFile — путь к файлу для логов аудита (JSONL).
	// Флаг: --audit-file | ENV: AUDIT_FILE
	AuditFile string

	// AuditURL — URL удалённого сервера-приёмника аудита.
	// Флаг: --audit-url | ENV: AUDIT_URL
	AuditURL string

	// EnableHTTPS — итоговое решение включать ли HTTPS: переменная ENABLE_HTTPS, затем явно
	// переданный -s, затем enable_https из файла, иначе false.
	// Флаг: -s | ENV: ENABLE_HTTPS | ключ файла: enable_https
	EnableHTTPS bool

	// CertFile — путь к PEM-сертификату TLS; создаётся выпуском самоподписанного, если отсутствует.
	// Флаг: -cert-file | ENV: CERT_FILE
	CertFile string

	// KeyFile — путь к PEM-закрытому ключу сертификата CertFile.
	// Флаг: -key-file | ENV: KEY_FILE
	KeyFile string

	// HTTPRedirectAddr — адрес HTTP-слушателя, который редиректит запросы на HTTPS.
	// Флаг: -http-addr | ENV: HTTP_SERVER_ADDRESS
	HTTPRedirectAddr string
)

// ParseFlags заполняет экспортированные переменные конфигурации пакета флагами,
// переменными окружения и JSON-файлом.
//
// Порядок приоритета для каждой настройки: непустая переменная окружения, затем явно
// переданный флаг, затем значение из файла, затем значение флага по умолчанию. Файл читается
// только если путь задан (-c, -config либо CONFIG); ошибки файла становятся предупреждением
// и настройкам не вредят — частичных значений из неразобранного файла не бывает.
//
// Ошибка возвращается, когда ENABLE_HTTPS непустая и не разбирается как логическое значение:
// включённый HTTPS не должен молча превращаться в открытый HTTP. Вызывается из main().
func ParseFlags() error {
	logrus.Info("Start ParseFlags")
	// как аргумент -a со значением :8080 по умолчанию
	flag.StringVar(&Addr, "a", ":8080", "адрес HTTP-сервера")
	flag.StringVar(&BaseURL, "b", "http://short.ru/", "base url")
	flag.StringVar(&FileName, "f", "data.json", "history file")
	flag.StringVar(&DatabaseDSN, "d", "", "DSN для подключения к PostgreSQL")
	flag.StringVar(&CookieKey, "k", "thisSuoerSecretMyKey", "секретный ключ для HMAC-подписи куки")
	flag.StringVar(&AuditFile, "audit-file", "", "путь к файлу для логов аудита")
	flag.StringVar(&AuditURL, "audit-url", "", "URL удалённого сервера-приёмника аудита")
	flag.BoolVar(&EnableHTTPS, "s", false, "включить HTTPS и редирект с HTTP")
	flag.StringVar(&CertFile, "cert-file", "cert.pem", "путь к PEM-сертификату TLS")
	flag.StringVar(&KeyFile, "key-file", "key.pem", "путь к PEM-закрытому ключу TLS")
	flag.StringVar(&HTTPRedirectAddr, "http-addr", ":8081", "адрес HTTP-слушателя редиректа на HTTPS")
	// оба имени пишут в одну переменную: -c и -config — синонимы одной настройки
	flag.StringVar(&ConfigFile, "c", "", "путь к JSON-файлу конфигурации")
	flag.StringVar(&ConfigFile, "config", "", "путь к JSON-файлу конфигурации")
	// парсим переданные серверу аргументы в зарегистрированные переменные
	flag.Parse()

	// Старшинство флага над файлом определяется самим набором флагов: сравнение со значением
	// по умолчанию не отличило бы осознанное -a :8080 от отсутствующего флага.
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

	envPath := os.Getenv(EnvConfig)
	doc := loadFileConfig(
		resolveString(envPath, set["c"] || set["config"], ConfigFile, ""),
		configSource(envPath, set),
	)

	// Неразбираемое ENABLE_HTTPS проверяем до заполнения прочих настроек: раз запуску пришёл
	// конец, ни одна настройка из файла и переменных не должна успеть примениться.
	enabled, err := resolveBool(os.Getenv(EnvEnableHTTPS), set["s"], EnableHTTPS, doc.EnableHTTPS)
	if err != nil {
		return err
	}

	EnableHTTPS = enabled

	// для случаев, когда в переменной окружения SERVER_ADDRESS присутствует непустое значение,
	// переопределим адрес запуска сервера,
	// даже если он был передан через аргумент командной строки
	Addr = resolveString(os.Getenv("SERVER_ADDRESS"), set["a"], Addr, doc.ServerAddress)
	BaseURL = resolveString(os.Getenv("BASE_URL"), set["b"], BaseURL, doc.BaseURL)
	FileName = resolveString(os.Getenv("FILE_STORAGE_PATH"), set["f"], FileName, doc.FileStoragePath)
	DatabaseDSN = resolveString(os.Getenv("DATABASE_DSN"), set["d"], DatabaseDSN, doc.DatabaseDSN)
	CookieKey = resolveString(os.Getenv("COOKIE_KEY"), set["k"], CookieKey, doc.CookieKey)
	AuditFile = resolveString(os.Getenv("AUDIT_FILE"), set["audit-file"], AuditFile, doc.AuditFile)
	AuditURL = resolveString(os.Getenv("AUDIT_URL"), set["audit-url"], AuditURL, doc.AuditURL)
	CertFile = resolveString(os.Getenv("CERT_FILE"), set["cert-file"], CertFile, doc.CertFile)
	KeyFile = resolveString(os.Getenv("KEY_FILE"), set["key-file"], KeyFile, doc.KeyFile)
	HTTPRedirectAddr = resolveString(os.Getenv("HTTP_SERVER_ADDRESS"), set["http-addr"], HTTPRedirectAddr, doc.HTTPServerAddress)

	logrus.Info("Read addr: ", Addr)
	logrus.Info("Read base: ", BaseURL)
	logrus.Info("DSN для подключения к PostgreSQL: ", DatabaseDSN)

	return nil
}

// resolveString выбирает значение строковой настройки: непустая переменная окружения,
// затем явно переданный флаг, затем значение из файла, затем значение флага.
//
// Пустая строка в переменной и в файле означает «источник молчит» — по тому же правилу,
// по которому непустая переменная переопределяет флаг.
func resolveString(envValue string, flagSet bool, flagValue, fileValue string) string {
	switch {
	case envValue != "":
		return envValue
	case flagSet:
		return flagValue
	case fileValue != "":
		return fileValue
	default:
		return flagValue
	}
}

// resolveBool выбирает значение логической настройки по тому же порядку источников.
//
// Значение переменной разбирает ParseEnabled: кривое значение — ошибка вызывающему коду,
// а не молчаливое «выключено». fileValue == nil означает, что ключа в файле нет; явный false
// из файла отличается от отсутствующего ключа, но остаётся младше флага и переменной.
func resolveBool(envValue string, flagSet, flagValue bool, fileValue *bool) (bool, error) {
	if envValue != "" {
		return ParseEnabled(flagValue, envValue)
	}

	if flagSet {
		return flagValue, nil
	}

	if fileValue != nil {
		return *fileValue, nil
	}

	return flagValue, nil
}

// ParseEnabled возвращает признак включения HTTPS: значение переменной окружения,
// если она непустая, иначе значение флага. Кривое значение переменной — ошибка,
// а не молчаливое «выключено»: иначе включённый HTTPS не должен превращаться в открытый HTTP.
func ParseEnabled(flagValue bool, envValue string) (bool, error) {
	if envValue == "" {
		return flagValue, nil
	}

	enabled, err := strconv.ParseBool(envValue)
	if err != nil {
		return false, fmt.Errorf("%s=%q: нужно логическое значение (1, t, true, 0, f, false): %w", EnvEnableHTTPS, envValue, err)
	}

	return enabled, nil
}
