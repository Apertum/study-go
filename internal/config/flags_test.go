package config

import (
	"flag"
	"io"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
)

// envNames — переменные окружения, которые читает ParseFlags. Тесты обязаны задавать их явно:
// унаследованное окружение процесса (в первую очередь CONFIG) не должно решать исход теста.
var envNames = []string{
	"SERVER_ADDRESS", "BASE_URL", "FILE_STORAGE_PATH", "DATABASE_DSN", "COOKIE_KEY",
	"AUDIT_FILE", "AUDIT_URL", EnvEnableHTTPS, "CERT_FILE", "KEY_FILE", "HTTP_SERVER_ADDRESS", EnvConfig,
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
