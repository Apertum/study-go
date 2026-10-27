// Package config предоставляет конфигурацию сервера URL-сократителя.
//
// Переменные пакета заполняются при вызове ParseFlags() из init():
//   - флаги командной строки (например, -a, -b, -d)
//   - переменные окружения, которые переопределяют флаги:
//     SERVER_ADDRESS, FILE_STORAGE_PATH, BASE_URL, DATABASE_DSN, COOKIE_KEY, AUDIT_FILE, AUDIT_URL,
//     HTTP_SERVER_ADDRESS, CERT_FILE, KEY_FILE
//
// Значение ENABLE_HTTPS разбирает ParseEnabled: ParseFlags() заполняет только переменную флага -s,
// потому что неразбираемое значение переменной обязано завершить процесс до прослушивания портов,
// а ParseFlags() ошибки не возвращает.
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
// Переменные окружения переопределяют значения флагов командной строки.
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

	// EnableHTTPS — значение флага -s: включать ли HTTPS.
	// Переменная ENABLE_HTTPS разбирается ParseEnabled.
	// Флаг: -s | ENV: ENABLE_HTTPS
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

// ParseFlags обрабатывает аргументы командной строки и переменные окружения,
// заполняя экспортированные переменные конфигурации пакета.
//
// Порядок приоритета: переменные окружения > флаги командной строки.
// Переменные окружения переопределяют флаги, если их значение не пустое.
//
// Вызывается из init() в main.go.
func ParseFlags() {

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
	// парсим переданные серверу аргументы в зарегистрированные переменные
	flag.Parse()

	// для случаев, когда в переменной окружения SERVER_ADDRESS присутствует непустое значение,
	// переопределим адрес запуска сервера,
	// даже если он был передан через аргумент командной строки
	if envRunAddr := os.Getenv("SERVER_ADDRESS"); envRunAddr != "" {
		Addr = envRunAddr
	}

	if env := os.Getenv("FILE_STORAGE_PATH"); env != "" {
		FileName = env
	}

	// для случаев, когда в переменной окружения BASE_URL присутствует непустое значение,
	// переопределим адрес запуска сервера,
	// даже если он был передан через аргумент командной строки
	if base := os.Getenv("BASE_URL"); base != "" {
		BaseURL = base
	}
	if envDSN := os.Getenv("DATABASE_DSN"); envDSN != "" {
		DatabaseDSN = envDSN
	}
	if envCookieKey := os.Getenv("COOKIE_KEY"); envCookieKey != "" {
		CookieKey = envCookieKey
	}
	if envAuditFile := os.Getenv("AUDIT_FILE"); envAuditFile != "" {
		AuditFile = envAuditFile
	}
	if envAuditURL := os.Getenv("AUDIT_URL"); envAuditURL != "" {
		AuditURL = envAuditURL
	}
	if env := os.Getenv("CERT_FILE"); env != "" {
		CertFile = env
	}
	if env := os.Getenv("KEY_FILE"); env != "" {
		KeyFile = env
	}
	if env := os.Getenv("HTTP_SERVER_ADDRESS"); env != "" {
		HTTPRedirectAddr = env
	}
	logrus.Info("Read addr: ", Addr)
	logrus.Info("Read base: ", BaseURL)
	logrus.Info("DSN для подключения к PostgreSQL: ", DatabaseDSN)
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
