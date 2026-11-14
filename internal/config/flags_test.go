package config

import (
	"bytes"
	"flag"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

// envNames — переменные окружения, которые читает ParseFlags. Тесты обязаны задавать их явно:
// унаследованное окружение процесса (в первую очередь CONFIG) не должно решать исход теста.
var envNames = []string{
	"SERVER_ADDRESS", "BASE_URL", "FILE_STORAGE_PATH", "DATABASE_DSN", "COOKIE_KEY",
	"AUDIT_FILE", "AUDIT_URL", EnvEnableHTTPS, "CERT_FILE", "KEY_FILE", "HTTP_SERVER_ADDRESS", EnvConfig,
	EnvTrustedSubnet,
}

// parseWith изображает запуск процесса: заново создаёт набор флагов и применяет аргументы с переменными.
// Переменные, которых нет в env, очищаются. Ошибка разбора считается падением теста.
//
// Вызывать только из последовательных тестов: ParseFlags заполняет переменные пакета.
func parseWith(t *testing.T, args []string, env map[string]string) {
	t.Helper()

	if err := runParse(t, args, env, io.Discard); err != nil {
		t.Fatalf("конфигурация не разобрана: %v", err)
	}
}

// runParse — тот же запуск с приёмником журнала и без реакции на ошибку: им проверяют
// предупреждения разбора файла и случай неразбираемого ENABLE_HTTPS.
func runParse(t *testing.T, args []string, env map[string]string, logOut io.Writer) error {
	t.Helper()

	for _, name := range envNames {
		if _, ok := env[name]; !ok {
			t.Setenv(name, "")
		}
	}

	for name, value := range env {
		t.Setenv(name, value)
	}

	oldArgs := os.Args
	oldFlags := flag.CommandLine
	oldOutput := logrus.StandardLogger().Out

	t.Cleanup(func() {
		os.Args = oldArgs
		flag.CommandLine = oldFlags
		logrus.SetOutput(oldOutput)
	})

	os.Args = append([]string{"shorter"}, args...)
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	logrus.SetOutput(logOut)

	return ParseFlags()
}

func TestParseFlagsHTTPSSettings(t *testing.T) {
	tests := []struct {
		env              map[string]string
		name             string
		wantCertFile     string
		wantKeyFile      string
		wantRedirectAddr string
		args             []string
		wantEnable       bool
	}{
		{
			name:             "без аргументов действуют значения по умолчанию",
			wantCertFile:     "cert.pem",
			wantKeyFile:      "key.pem",
			wantRedirectAddr: ":8081",
		},
		{
			name:             "флаги задают настройки",
			args:             []string{"-s", "-cert-file", "flag-cert.pem", "-key-file", "flag-key.pem", "-http-addr", ":9091"},
			wantEnable:       true,
			wantCertFile:     "flag-cert.pem",
			wantKeyFile:      "flag-key.pem",
			wantRedirectAddr: ":9091",
		},
		{
			name: "переменные задают настройки без флагов",
			env: map[string]string{
				"ENABLE_HTTPS":        "true",
				"CERT_FILE":           "env-cert.pem",
				"KEY_FILE":            "env-key.pem",
				"HTTP_SERVER_ADDRESS": ":9092",
			},
			wantEnable:       true,
			wantCertFile:     "env-cert.pem",
			wantKeyFile:      "env-key.pem",
			wantRedirectAddr: ":9092",
		},
		{
			name: "переменная переопределяет флаг",
			args: []string{"-s", "-cert-file", "flag-cert.pem", "-key-file", "flag-key.pem", "-http-addr", ":9091"},
			env: map[string]string{
				"ENABLE_HTTPS":        "0",
				"CERT_FILE":           "env-cert.pem",
				"KEY_FILE":            "env-key.pem",
				"HTTP_SERVER_ADDRESS": ":9092",
			},
			wantEnable:       false,
			wantCertFile:     "env-cert.pem",
			wantKeyFile:      "env-key.pem",
			wantRedirectAddr: ":9092",
		},
		{
			name: "пустая переменная сохраняет флаг",
			args: []string{"-s", "-cert-file", "flag-cert.pem", "-key-file", "flag-key.pem", "-http-addr", ":9091"},
			env: map[string]string{
				"ENABLE_HTTPS":        "",
				"CERT_FILE":           "",
				"KEY_FILE":            "",
				"HTTP_SERVER_ADDRESS": "",
			},
			wantEnable:       true,
			wantCertFile:     "flag-cert.pem",
			wantKeyFile:      "flag-key.pem",
			wantRedirectAddr: ":9091",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parseWith(t, tt.args, tt.env)

			if EnableHTTPS != tt.wantEnable {
				t.Errorf("HTTPS включён=%v, ожидали %v", EnableHTTPS, tt.wantEnable)
			}

			if CertFile != tt.wantCertFile {
				t.Errorf("сертификат %q, ожидали %q", CertFile, tt.wantCertFile)
			}

			if KeyFile != tt.wantKeyFile {
				t.Errorf("ключ %q, ожидали %q", KeyFile, tt.wantKeyFile)
			}

			if HTTPRedirectAddr != tt.wantRedirectAddr {
				t.Errorf("адрес редиректа %q, ожидали %q", HTTPRedirectAddr, tt.wantRedirectAddr)
			}
		})
	}
}

// TestParseTrustedSubnet сверяет разбор настройки: пустое значение означает «не задано»,
// адрес без префикса длины и мусор дают ошибку с именем источника и самим значением.
func TestParseTrustedSubnet(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "пустое значение — настройка не задана", raw: "", want: ""},
		{name: "сеть IPv4", raw: "10.0.0.0/8", want: "10.0.0.0/8"},
		{name: "сеть IPv6", raw: "fd00::/8", want: "fd00::/8"},
		{name: "один адрес под маской /32", raw: "192.168.1.7/32", want: "192.168.1.7/32"},
		{name: "адрес без префикса длины", raw: "10.0.0.1", wantErr: true},
		{name: "не адрес", raw: "not-a-cidr", wantErr: true},
		{name: "мусор после маски", raw: "10.0.0.0/8x", wantErr: true},
		{name: "маска шире адреса IPv4", raw: "10.0.0.0/33", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTrustedSubnet("-t", tt.raw)

			switch {
			case tt.wantErr && err == nil:
				t.Fatalf("ожидали ошибку разбора %q", tt.raw)
			case !tt.wantErr && err != nil:
				t.Fatalf("неожиданная ошибка для %q: %v", tt.raw, err)
			case tt.wantErr:
				if !strings.Contains(err.Error(), "-t") || !strings.Contains(err.Error(), tt.raw) {
					t.Errorf("в ошибке %q нет источника и значения настройки", err)
				}

				return
			}

			gotValue := ""
			if got.IsValid() {
				gotValue = got.String()
			}

			if gotValue != tt.want {
				t.Errorf("сеть %q, ожидали %q", gotValue, tt.want)
			}
		})
	}
}

// TestParseFlagsBadTrustedSubnetFromFlag фиксирует, что кривое значение флага прерывает разбор
// до применения прочих настроек — как неразбираемый ENABLE_HTTPS.
func TestParseFlagsBadTrustedSubnetFromFlag(t *testing.T) {
	err := runParse(t, []string{"-t", "10.0.0.1"}, map[string]string{"SERVER_ADDRESS": ":6060"}, io.Discard)
	if err == nil {
		t.Fatal("ожидали ошибку неразбираемого значения флага -t")
	}

	if !strings.Contains(err.Error(), "-t") || !strings.Contains(err.Error(), "10.0.0.1") {
		t.Errorf("в ошибке %q нет источника и значения настройки", err)
	}

	if Addr != ":8080" || TrustedSubnet.IsValid() {
		t.Errorf("настройки применены до ошибки: адрес %q, сеть %s", Addr, TrustedSubnet)
	}
}

// TestParseFlagsBadTrustedSubnetFromEnv — переменная остаётся старшим источником и называется
// в ошибке, даже когда файл задаёт корректную сеть.
func TestParseFlagsBadTrustedSubnetFromEnv(t *testing.T) {
	path := writeConfig(t, subnetConfig("10.0.0.0/8"))

	err := runParse(t, []string{"-c", path}, map[string]string{EnvTrustedSubnet: "все"}, io.Discard)
	if err == nil {
		t.Fatal("ожидали ошибку неразбираемого значения переменной")
	}

	if !strings.Contains(err.Error(), EnvTrustedSubnet) {
		t.Errorf("в ошибке %q нет имени переменной", err)
	}
}

// TestParseFlagsBadTrustedSubnetFromFile — значение из файла прерывает запуск так же, источник
// указан ключом файла, а секретные строки того же файла в текст ошибки не попадают.
func TestParseFlagsBadTrustedSubnetFromFile(t *testing.T) {
	path := writeConfig(t, `{"trusted_subnet": "not-a-cidr", "cookie_key": "`+secretCookie+`", "database_dsn": "`+secretDSN+`"}`)

	err := runParse(t, []string{"-c", path}, nil, io.Discard)
	if err == nil {
		t.Fatal("ожидали ошибку неразбираемого значения из файла")
	}

	if !strings.Contains(err.Error(), "trusted_subnet") || !strings.Contains(err.Error(), "not-a-cidr") {
		t.Errorf("в ошибке %q нет ключа файла и значения", err)
	}

	if strings.Contains(err.Error(), secretCookie) || strings.Contains(err.Error(), "parol-iz-fayla") {
		t.Errorf("в ошибке %q оказалось содержимое файла", err)
	}
}

// TestParseFlagsBrokenFileLeavesSubnetUnset разделяет два разных правила: неразобранный файл —
// предупреждение и пустая сеть (эндпоинт закрыт), а неразбираемое значение — ошибка запуска.
func TestParseFlagsBrokenFileLeavesSubnetUnset(t *testing.T) {
	var log bytes.Buffer

	path := writeConfig(t, `{"trusted_subnet": "10.0.0.0/8", // профиль`+"\n")

	if err := runParse(t, []string{"-c", path}, nil, &log); err != nil {
		t.Fatalf("битый файл не должен прерывать запуск: %v", err)
	}

	if !strings.Contains(log.String(), "конфигурация из файла не применена") {
		t.Errorf("предупреждения о файле нет: %q", log.String())
	}

	if TrustedSubnet.IsValid() {
		t.Errorf("сеть %s взята из неразобранного файла", TrustedSubnet)
	}
}
