package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"strings"

	"github.com/sirupsen/logrus"
	config "study-go.ru/cho/eto/internal/config"
)

type NetAddress struct {
	Host string
	Port int
}

func init() {
	config.LogsInit()
}

func (a NetAddress) String() string {
	return "http://" + a.Host + ":" + strconv.Itoa(a.Port)
}

func (a *NetAddress) Set(s string) error {
	hp := strings.Split(s, ":")
	if len(hp) != 2 {
		return fmt.Errorf("Нужен адрес:порт, недопустимое значение: n=%s", s)
	}
	port, err := strconv.Atoi(hp[1])
	if err != nil {
		return err
	}
	a.Host = hp[0]
	a.Port = port
	return nil
}

func main() {
	addr := new(NetAddress)
	// если интерфейс не реализован,
	// здесь будет ошибка компиляции
	_ = flag.Value(addr)
	// проверка реализации
	flag.Var(addr, "addr", "Net address host:port")
	flag.Parse()
	logrus.Debug(addr.Host)
	logrus.Debug(addr.Port)

	// приглашение в консоли
	fmt.Print("Введите длинный URL: ")
	// открываем потоковое чтение из консоли
	reader := bufio.NewReader(os.Stdin)
	// читаем строку из консоли
	long, err := reader.ReadString('\n')
	if err != nil {
		logrus.WithError(err).Fatal("Ошибка чтения из консоли")
		os.Exit(1)
	}
	long = strings.TrimSuffix(long, "\n")

	// контейнер данных для запроса
	var req struct {
		URL string `json:"url"`
	}
	// заполняем контейнер данными
	req.URL = long
	data, err := json.Marshal(req)
	if err != nil {
		logrus.WithError(err).Fatal("Ошибка сериализации данных запроса")
		os.Exit(1)
	}

	// добавляем HTTP-клиент
	client := &http.Client{}
	// пишем запрос
	// запрос методом POST должен, помимо заголовков, содержать тело
	// тело должно быть источником потокового чтения io.Reader
	request, err := http.NewRequest(http.MethodPost, addr.String(), strings.NewReader(string(data)))
	if err != nil {
		logrus.WithError(err).Fatal("Ошибка создания HTTP-запроса")
		os.Exit(1)
	}
	// в заголовках запроса указываем кодировку
	request.Header.Add("Content-Type", "application/json")
	// отправляем запрос и получаем ответ
	response, err := client.Do(request)
	if err != nil {
		logrus.WithError(err).Fatal("Ошибка отправки HTTP-запроса")
		os.Exit(1)
	}
	// выводим код ответа
	logrus.Info("Статус-код ", response.Status)
	defer response.Body.Close()
	// читаем поток из тела ответа
	body, err := io.ReadAll(response.Body)
	if err != nil {
		logrus.WithError(err).Fatal("Ошибка чтения тела ответа")
		os.Exit(1)
	}
	// и печатаем его
	logrus.Info(string(body))
}
