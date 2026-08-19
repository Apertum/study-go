package config

import (
	"flag"
	"os"

	"github.com/sirupsen/logrus"
)

// экспортированная переменная
var BaseURL string
var Addr string
var FileName string = "dataNN.json"
var DatabaseDSN string
var CookieKey string

// ParseFlags обрабатывает аргументы командной строки
// и сохраняет их значения в соответствующих переменных
func ParseFlags() {

	logrus.Info("Start ParseFlags")
	// как аргумент -a со значением :8080 по умолчанию
	flag.StringVar(&Addr, "a", ":8080", "адрес HTTP-сервера")
	flag.StringVar(&BaseURL, "b", "http://short.ru/", "base url")
	flag.StringVar(&FileName, "f", "data.json", "history file")
	flag.StringVar(&DatabaseDSN, "d", "", "DSN для подключения к PostgreSQL")
	flag.StringVar(&CookieKey, "k", "thisSuoerSecretMyKey", "секретный ключ для HMAC-подписи куки")
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
	logrus.Info("Read addr: ", Addr)
	logrus.Info("Read base: ", BaseURL)
	logrus.Info("DSN для подключения к PostgreSQL: ", DatabaseDSN)
}
