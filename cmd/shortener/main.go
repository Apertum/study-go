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
	"study-go.ru/cho/eto/internal/secure"
)

// Информация о сборке, подставляется при линковке через -ldflags "-X main.buildVersion=... -X main.buildDate=... -X main.buildCommit=...":
//
//	go run -ldflags "-X main.buildVersion=v1.0.0 -X main.buildDate=2026-10-22 -X main.buildCommit=abc1234" ./cmd/shortener -addr localhost:8080
var (
	buildVersion string = "N/A"
	buildDate    string = "N/A"
	buildCommit  string = "N/A"
)

type NetAddress struct {
	Host string
	Port int
}

// Настройки доверия клиента: сертификат сервера берётся из файла, переменная переопределяет флаг.
const (
	defaultCertFile = "cert.pem"
	envCertFile     = "CERT_FILE"
)

// String возвращает значение флага -addr для показа в справке: схема адреса там не участвует.
func (a NetAddress) String() string {
	return a.Host + ":" + strconv.Itoa(a.Port)
}

// URL возвращает адрес службы со схемой https при включённом HTTPS.
func (a NetAddress) URL(useHTTPS bool) string {
	scheme := "http"
	if useHTTPS {
		scheme = "https"
	}

	return scheme + "://" + a.Host + ":" + strconv.Itoa(a.Port)
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
	config.LogsInit()
	fmt.Printf("Build version: %s\n", buildVersion)
	fmt.Printf("Build date: %s\n", buildDate)
	fmt.Printf("Build commit: %s\n", buildCommit)
	addr := new(NetAddress)
	// если интерфейс не реализован,
	// здесь будет ошибка компиляции
	_ = flag.Value(addr)
	// проверка реализации
	flag.Var(addr, "addr", "Net address host:port")
	useHTTPSFlag := flag.Bool("s", false, "запрос по HTTPS с проверкой сертификата из -cert-file")
	certFileFlag := flag.String("cert-file", defaultCertFile, "путь к PEM-сертификату сервера, которому доверяем")
	flag.Parse()
	logrus.Debug(addr.Host)
	logrus.Debug(addr.Port)

	useHTTPS, err := config.ParseEnabled(*useHTTPSFlag, os.Getenv(config.EnvEnableHTTPS))
	if err != nil {
		logrus.WithError(err).Fatalf("Не разобрано значение %s", config.EnvEnableHTTPS)
		os.Exit(1)
	}

	certFile := *certFileFlag
	if env := os.Getenv(envCertFile); env != "" {
		certFile = env
	}

	client, err := newClient(useHTTPS, certFile)
	if err != nil {
		logrus.WithError(err).Fatalf("HTTP-клиент не настроен: не прочитан сертификат -cert-file либо %s", envCertFile)
		os.Exit(1)
	}

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
	// пишем запрос
	// запрос методом POST должен, помимо заголовков, содержать тело
	// тело должно быть источником потокового чтения io.Reader
	request, err := http.NewRequest(http.MethodPost, addr.URL(useHTTPS), strings.NewReader(string(data)))
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

// newClient собирает HTTP-клиент: по HTTPS он доверяет только сертификату из certFile,
// по обычному HTTP клиент остаётся прежним и файлы не читает.
func newClient(useHTTPS bool, certFile string) (*http.Client, error) {
	if !useHTTPS {
		return &http.Client{}, nil
	}

	tlsConfig, err := secure.ClientTLS(certFile)
	if err != nil {
		return nil, err
	}

	return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}, nil
}
