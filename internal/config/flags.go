package config

import (
	"flag"
	"os"

	"github.com/sirupsen/logrus"
)

// экспортированная переменная
var BaseUrl string
var Addr string

// ParseFlags обрабатывает аргументы командной строки
// и сохраняет их значения в соответствующих переменных
func ParseFlags() {

	logrus.Info("Start ParseFlags")
	// как аргумент -a со значением :8080 по умолчанию
	flag.StringVar(&Addr, "a", ":8080", "адрес HTTP-сервера")
	flag.StringVar(&BaseUrl, "b", "http://short.ru/", "base url")
	// парсим переданные серверу аргументы в зарегистрированные переменные
	flag.Parse()

	// для случаев, когда в переменной окружения SERVER_ADDRESS присутствует непустое значение,
	// переопределим адрес запуска сервера,
	// даже если он был передан через аргумент командной строки
	if envRunAddr := os.Getenv("SERVER_ADDRESS"); envRunAddr != "" {
		Addr = envRunAddr
	}

	// для случаев, когда в переменной окружения RUN_ADDR присутствует непустое значение,
	// переопределим адрес запуска сервера,
	// даже если он был передан через аргумент командной строки
	if base := os.Getenv("RUN_ADDR"); base != "" {
		BaseUrl = base
	}
	logrus.Info("Read addr: ", Addr)
	logrus.Info("Read base: ", BaseUrl)
}
