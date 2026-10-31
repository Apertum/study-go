package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"study-go.ru/cho/eto/internal/config"
	"study-go.ru/cho/eto/internal/secure"
)

func TestNetAddressURL(t *testing.T) {
	tests := []struct {
		name     string
		want     string
		address  NetAddress
		useHTTPS bool
	}{
		{name: "обычный HTTP", address: NetAddress{Host: "localhost", Port: 8080}, want: "http://localhost:8080"},
		{name: "HTTPS", address: NetAddress{Host: "localhost", Port: 8080}, useHTTPS: true, want: "https://localhost:8080"},
		{name: "адрес по IP сохраняется", address: NetAddress{Host: "127.0.0.1", Port: 18443}, useHTTPS: true, want: "https://127.0.0.1:18443"},
		{name: "нулевое значение", want: "http://:0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.address.URL(tt.useHTTPS); got != tt.want {
				t.Errorf("URL(%v) = %q, ожидали %q", tt.useHTTPS, got, tt.want)
			}
		})
	}
}

// TestNetAddressStringValue проверяет, что справка флага -addr по-прежнему показывает адрес без схемы.
func TestNetAddressStringValue(t *testing.T) {
	address := NetAddress{Host: "localhost", Port: 8080}

	if got := address.String(); got != "localhost:8080" {
		t.Errorf("String() = %q, ожидали %q", got, "localhost:8080")
	}
}

func TestNetAddressSet(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		wantHost string
		wantErr  string
		wantPort int
	}{
		{name: "имя и порт", value: "localhost:8080", wantHost: "localhost", wantPort: 8080},
		{name: "адрес и порт", value: "127.0.0.1:1", wantHost: "127.0.0.1", wantPort: 1},
		{name: "без порта", value: "localhost", wantErr: "недопустимое значение"},
		{name: "порт не число", value: "localhost:abc", wantErr: "invalid syntax"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var address NetAddress

			err := address.Set(tt.value)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ожидалась ошибка для %q", tt.value)
				}

				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("в тексте %q нет %q", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}

			if address.Host != tt.wantHost || address.Port != tt.wantPort {
				t.Errorf("разобрано %s:%d, ожидали %s:%d", address.Host, address.Port, tt.wantHost, tt.wantPort)
			}
		})
	}
}

func TestNewClient(t *testing.T) {
	t.Run("без HTTPS файлы не читаются", func(t *testing.T) {
		client, err := newClient(false, filepath.Join(t.TempDir(), "нет-такого.pem"))
		if err != nil {
			t.Fatalf("неожиданная ошибка: %v", err)
		}

		if client.Transport != nil {
			t.Error("обычному клиенту навязан собственный транспорт")
		}
	})

	t.Run("для HTTPS нужен сертификат", func(t *testing.T) {
		certPath := filepath.Join(t.TempDir(), "нет-такого.pem")

		client, err := newClient(true, certPath)
		if err == nil {
			t.Fatalf("ожидалась ошибка, получили клиент %+v", client)
		}

		if !strings.Contains(err.Error(), certPath) {
			t.Errorf("в тексте %q нет пути %q", err, certPath)
		}
	})
}

// todo check test in Windows
func TestShortenerClientTrustsIssuedCertificate(t *testing.T) {
	material, certPath := issueMaterial(t)

	const (
		wantStatus = http.StatusCreated
		wantBody   = "короткий адрес готов"
	)

	server := startMaterialServer(t, material, wantStatus, wantBody)

	stdout, code := runClient(t, buildClient(t), t.TempDir(), clientRun{
		args:  []string{"-addr", server, "-s", "-cert-file", certPath},
		stdin: "http://example.com/long\n",
	})

	if code != 0 {
		t.Fatalf("клиент завершился кодом %d:\n%s", code, stdout)
	}

	if !strings.Contains(stdout, fmt.Sprintf("Статус-код %d Created", wantStatus)) {
		t.Errorf("в выводе %q нет статуса %d", stdout, wantStatus)
	}

	if !strings.Contains(stdout, wantBody) {
		t.Errorf("в выводе %q нет тела ответа %q", stdout, wantBody)
	}
}

// todo check test in Windows
func TestShortenerClientRejectsUntrustedServer(t *testing.T) {
	_, certPath := issueMaterial(t)
	foreignMaterial, _ := issueMaterial(t)

	bin := buildClient(t)

	tests := []struct {
		name    string
		address string
	}{
		{name: "чужой самоподписанный сертификат", address: startMaterialServer(t, foreignMaterial, http.StatusCreated, "не для нас")},
		{name: "открытый HTTP по адресу HTTPS", address: startPlainServer(t, http.StatusCreated, "открытым текстом")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()

			stdout, code := runClient(t, bin, dir, clientRun{
				args:  []string{"-addr", tt.address, "-s", "-cert-file", certPath},
				stdin: "http://example.com/long\n",
			})

			if code == 0 {
				t.Fatalf("клиент завершился успешно, хотя сервер недоверенный:\n%s", stdout)
			}

			if !strings.Contains(stdout, "Ошибка отправки HTTP-запроса") {
				t.Errorf("в выводе %q нет сообщения об ошибке отправки", stdout)
			}

			reason := readChildLog(t, dir)
			if !strings.Contains(reason, "x509") && !strings.Contains(reason, "tls") && !strings.Contains(reason, "http:") {
				t.Errorf("в журнале %q нет причины отказа", reason)
			}
		})
	}
}

// todo check test in Windows
func TestShortenerClientReportsBadEnableValue(t *testing.T) {
	certPath := filepath.Join(t.TempDir(), "cert.pem")

	stdout, code := runClient(t, buildClient(t), t.TempDir(), clientRun{
		args: []string{"-addr", "127.0.0.1:1", "-cert-file", certPath},
		env:  map[string]string{config.EnvEnableHTTPS: "нет"},
	})

	if code == 0 {
		t.Fatalf("клиент завершился успешно при %s=нет:\n%s", config.EnvEnableHTTPS, stdout)
	}

	if !strings.Contains(stdout, config.EnvEnableHTTPS) {
		t.Errorf("в выводе %q нет имени переменной %s", stdout, config.EnvEnableHTTPS)
	}

	if strings.Contains(stdout, "Ошибка отправки HTTP-запроса") {
		t.Error("запрос ушёл хотя конфигурация не разобрана")
	}
}

// todo check test in Windows
func TestShortenerClientPrefersEnvironmentOverFlags(t *testing.T) {
	material, certPath := issueMaterial(t)
	_, otherCert := issueMaterial(t)

	server := startMaterialServer(t, material, http.StatusCreated, "переменная важнее флага")

	// Флаг указывает на чужой сертификат, переменная CERT_FILE — на нужный.
	stdout, code := runClient(t, buildClient(t), t.TempDir(), clientRun{
		args:  []string{"-addr", server, "-s", "-cert-file", otherCert},
		stdin: "http://example.com/long\n",
		env:   map[string]string{envCertFile: certPath},
	})

	if code != 0 {
		t.Fatalf("клиент завершился кодом %d, переменная %s не переопределила флаг:\n%s", code, envCertFile, stdout)
	}
}

// todo check test in Windows
func TestShortenerClientHelpMentionsHTTPSSettings(t *testing.T) {
	stdout, code := runClient(t, buildClient(t), t.TempDir(), clientRun{args: []string{"-h"}})

	if code != 0 {
		t.Fatalf("справка завершилась кодом %d:\n%s", code, stdout)
	}

	for _, want := range []string{"-addr", "-s", "-cert-file"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в справке нет флага %s:\n%s", want, stdout)
		}
	}
}

// clientRun — параметры одного запуска собранного клиента.
type clientRun struct {
	env   map[string]string
	stdin string
	args  []string
}

// buildTarget возвращает путь, куда `go build -o` положит бинарник: на Windows это всегда
// имя с .exe, а запуск пути без расширения не работает — os/exec не дописывает его сам.
func buildTarget(dir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	return filepath.Join(dir, name)
}

// buildClient собирает бинарник клиента в отдельный временный каталог.
func buildClient(t *testing.T) string {
	t.Helper()

	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("нужен инструмент go для сборки клиента")
	}

	dir := t.TempDir()
	bin := buildTarget(dir, "shortener")

	build := exec.Command(goTool, "build", "-o", bin, ".")

	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("клиент не собран: %v\n%s", err, out)
	}

	return bin
}

// runClient запускает клиента в временном каталоге и возвращает объединённый вывод с кодом возврата.
func runClient(t *testing.T, bin, dir string, run clientRun) (string, int) {
	t.Helper()

	cmd := exec.Command(bin, run.args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(run.stdin)
	cmd.Env = childEnv(run.env)

	out := &bytes.Buffer{}
	cmd.Stdout = out
	cmd.Stderr = out

	err := cmd.Run()

	code := 0

	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	case errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EACCES):
		t.Skipf("каталог %s запрещает исполнение собранных бинарников — укажи TMPDIR на исполняемый каталог", dir)
	default:
		t.Fatalf("клиент не запущен: %v", err)
	}

	return out.String(), code
}

// childEnv собирает окружение дочернего процесса: настройки HTTPS наследуются только явно.
func childEnv(values map[string]string) []string {
	skip := map[string]bool{config.EnvEnableHTTPS: true, envCertFile: true}

	env := make([]string, 0, len(os.Environ())+len(values))

	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if skip[name] {
			continue
		}

		env = append(env, item)
	}

	for name, value := range values {
		env = append(env, name+"="+value)
	}

	return env
}

// readChildLog читает журнал, который дочерний процесс пишет рядом с рабочим каталогом.
func readChildLog(t *testing.T, dir string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(dir, "logs", "app.log"))
	if err != nil {
		t.Fatalf("журнал дочернего процесса не читается: %v", err)
	}

	return string(content)
}

// issueMaterial выпускает пару во временном каталоге и возвращает материал с путём сертификата.
func issueMaterial(t *testing.T) (secure.Material, string) {
	t.Helper()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")

	material, err := secure.LoadOrGenerate(certPath, filepath.Join(dir, "key.pem"))
	if err != nil {
		t.Fatalf("материалы не выпущены: %v", err)
	}

	return material, certPath
}

// startMaterialServer поднимает TLS-сервер на выпущенном сертификате и возвращает его адрес.
func startMaterialServer(t *testing.T, material secure.Material, status int, body string) string {
	t.Helper()

	server := httptest.NewUnstartedServer(responseHandler(status, body))
	server.TLS = material.ServerTLS()
	server.StartTLS()

	t.Cleanup(server.Close)

	return server.Listener.Addr().String()
}

// startPlainServer поднимает обычный HTTP-сервер и возвращает его адрес.
func startPlainServer(t *testing.T, status int, body string) string {
	t.Helper()

	server := httptest.NewServer(responseHandler(status, body))
	t.Cleanup(server.Close)

	return server.Listener.Addr().String()
}

// responseHandler отвечает фиксированным кодом и телом.
func responseHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}
