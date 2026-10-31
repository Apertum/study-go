package secure_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"study-go.ru/cho/eto/internal/secure"
)

// privateKeyMark — PEM-заголовок закрытого ключа, который не должен появляться в выводе процесса.
const privateKeyMark = "PRIVATE KEY"

// lockedBuffer накапливает вывод процесса и безопасен для чтения во время записи.
type lockedBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

// String возвращает накопленный вывод.
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// serverProcess — запущенный для проверки процесс сервера с перехватом стандартных потоков.
type serverProcess struct {
	stderr  *lockedBuffer
	done    chan error
	stopped chan struct{}
	stop    func()
}

// alive проверяет, что процесс сервера ещё работает. сигналом 0 это не проверяется:
// на Windows os.Process.Signal умеет только убивать процесс.
func (p *serverProcess) alive() bool {
	select {
	case <-p.stopped:
		return false
	default:
		return true
	}
}

// wait дожидается завершения процесса и возвращает результат его запуска.
func (p *serverProcess) wait() error {
	return <-p.done
}

// buildTarget возвращает путь, куда `go build -o` положит бинарник: на Windows это всегда
// имя с .exe, а запуск пути без расширения не работает — os/exec не дописывает его сам.
func buildTarget(dir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	return filepath.Join(dir, name)
}

// buildServer собирает бинарник сервера во временный каталог теста.
func buildServer(t *testing.T) string {
	t.Helper()

	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("нужен инструмент go для сборки сервера")
	}

	bin := buildTarget(t.TempDir(), "shorter")

	build := exec.Command(goTool, "build", "-o", bin, "../../cmd/shorter")

	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("сервер не собран: %v\n%s", err, out)
	}

	return bin
}

// freeAddr возвращает заведомо свободный адрес на loopback.
func freeAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("свободный порт не получен: %v", err)
	}

	addr := listener.Addr().String()

	if err := listener.Close(); err != nil {
		t.Errorf("слушатель не закрыт: %v", err)
	}

	return addr
}

// startServer запускает сервер в временном каталоге: относительные пути логов и материалов остаются внутри него.
func startServer(t *testing.T, bin, dir string, env map[string]string) *serverProcess {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = dir
	cmd.Stdout = io.Discard
	cmd.Env = os.Environ()

	for name, value := range env {
		cmd.Env = append(cmd.Env, name+"="+value)
	}

	stderr := &lockedBuffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		cancel()

		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("каталог %s запрещает исполнение собранных бинарников — укажи TMPDIR на исполняемый каталог", dir)
		}

		t.Fatalf("сервер не запущен: %v", err)
	}

	// Один Wait на процесс: ошибку получает дождавшийся, а закрытие stopped видно всем.
	done := make(chan error, 1)
	stopped := make(chan struct{})

	go func() {
		err := cmd.Wait()
		done <- err
		close(stopped)
	}()

	stop := func() {
		cancel()

		select {
		case <-stopped:
		case <-time.After(20 * time.Second):
			t.Error("процесс сервера не завершился после отмены контекста")
		}
	}

	t.Cleanup(stop)

	return &serverProcess{stderr: stderr, done: done, stopped: stopped, stop: stop}
}

// waitResponse допрашивается до первого HTTP-ответа любого кода.
func waitResponse(t *testing.T, client *http.Client, url string, proc *serverProcess) *http.Response {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)

	var lastErr error

	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, url, nil)
		if err != nil {
			t.Fatalf("запрос не собран: %v", err)
		}

		resp, err := client.Do(req)
		if err == nil {
			return resp
		}

		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("сервер не ответил по %s: %v\nвывод процесса:\n%s", url, lastErr, proc.stderr.String())

	return nil
}

// waitMaterial дожидается появления файла материалов TLS.
func waitMaterial(t *testing.T, path string, proc *serverProcess) {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)

	var lastErr error

	for time.Now().Before(deadline) {
		info, err := os.Stat(path)
		if err == nil && info.Size() > 0 {
			return
		}

		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("материал %s не появился: %v\nвывод процесса:\n%s", path, lastErr, proc.stderr.String())
}

func TestShorterServerServesHTTPSAndRedirect(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildServer(t)
	dir := t.TempDir()

	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	tlsAddr := freeAddr(t)
	redirectAddr := freeAddr(t)

	env := map[string]string{
		"SERVER_ADDRESS":      tlsAddr,
		"HTTP_SERVER_ADDRESS": redirectAddr,
		"ENABLE_HTTPS":        "true",
		"CERT_FILE":           certPath,
		"KEY_FILE":            keyPath,
	}

	proc := startServer(t, bin, dir, env)

	waitMaterial(t, certPath, proc)
	waitMaterial(t, keyPath, proc)

	resp := waitResponse(t, httpsClient(t, certPath), "https://"+tlsAddr+"/ping", proc)
	defer func() { _ = resp.Body.Close() }()

	if resp.TLS == nil {
		t.Fatal("ответ получен без TLS-соединения")
	}

	if resp.TLS.Version < tls.VersionTLS12 {
		t.Errorf("соединение договорилось о версии %#x, ожидали не ниже TLS 1.2", resp.TLS.Version)
	}

	wantLocation := "https://" + tlsAddr + "/api/user/urls?page=2"
	if got := redirectLocation(t, redirectAddr, "/api/user/urls?page=2"); got != wantLocation {
		t.Errorf("Location %q, ожидали %q", got, wantLocation)
	}

	// Открытый HTTP на TLS-порту: net/http отвечает HTTP/1.0 400 ещё до обработчиков либо ошибка соединения.
	plainClient := &http.Client{Timeout: 3 * time.Second}

	plainResp, plainErr := plainClient.Get("http://" + tlsAddr + "/ping")
	if plainErr == nil {
		_ = plainResp.Body.Close()

		if plainResp.StatusCode != http.StatusBadRequest {
			t.Errorf("открытый HTTP на TLS-порту обслужен кодом %d %s, хотя не должен",
				plainResp.StatusCode, plainResp.Proto)
		}
	}

	if !proc.alive() {
		t.Fatalf("процесс сервера завершился после ошибки клиента:\n%s", proc.stderr.String())
	}

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("сертификат не записан: %v", err)
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("ключ не записан: %v", err)
	}

	assertNoSecretsInProcessOutput(t, proc.stderr.String(), filepath.Join(dir, "logs", "app.log"), keyPEM)

	proc.stop()

	restart := startServer(t, bin, dir, env)
	restartResp := waitResponse(t, httpsClient(t, certPath), "https://"+tlsAddr+"/ping", restart)
	_ = restartResp.Body.Close()

	restartCertPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("сертификат после перезапуска не читается: %v", err)
	}

	restartKeyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("ключ после перезапуска не читается: %v", err)
	}

	if !bytes.Equal(certPEM, restartCertPEM) || !bytes.Equal(keyPEM, restartKeyPEM) {
		t.Error("перезапуск перевыпустил материалы TLS")
	}
}

func TestShorterServerWithoutHTTPS(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildServer(t)
	dir := t.TempDir()

	plainAddr := freeAddr(t)
	redirectAddr := freeAddr(t)

	proc := startServer(t, bin, dir, map[string]string{
		"SERVER_ADDRESS":      plainAddr,
		"HTTP_SERVER_ADDRESS": redirectAddr,
	})

	client := &http.Client{Timeout: 3 * time.Second}

	resp := waitResponse(t, client, "http://"+plainAddr+"/ping", proc)
	defer func() { _ = resp.Body.Close() }()

	if resp.TLS != nil {
		t.Error("обычный HTTP-ответ содержит TLS-состояние")
	}

	conn, err := net.DialTimeout("tcp", redirectAddr, 3*time.Second)
	if err == nil {
		_ = conn.Close()
		t.Errorf("адрес редиректа %s занят при выключенном HTTPS", redirectAddr)
	}

	for _, name := range []string{"cert.pem", "key.pem"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("при выключенном HTTPS появился файл %s", name)
		}
	}
}

func TestShorterServerRejectsSameAddresses(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildServer(t)
	dir := t.TempDir()

	addr := freeAddr(t)

	proc := startServer(t, bin, dir, map[string]string{
		"SERVER_ADDRESS":      addr,
		"HTTP_SERVER_ADDRESS": addr,
		"ENABLE_HTTPS":        "1",
		"CERT_FILE":           filepath.Join(dir, "cert.pem"),
		"KEY_FILE":            filepath.Join(dir, "key.pem"),
	})

	if err := proc.wait(); err == nil {
		t.Fatalf("сервер завершился успешно с совпадающими адресами %s\n%s", addr, proc.stderr.String())
	}

	if text := proc.stderr.String(); !strings.Contains(text, addr) ||
		!strings.Contains(text, "адрес редиректа") || !strings.Contains(text, "основным адресом") {
		t.Errorf("в выводе %q нет обоих адресов с указанием роли (%s)", text, addr)
	}

	for _, name := range []string{"cert.pem", "key.pem"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("при ошибке конфигурации появился файл %s", name)
		}
	}
}

// httpsClient возвращает клиента, которая доверяет выпущенному сертификату.
func httpsClient(t *testing.T, certPath string) *http.Client {
	t.Helper()

	cfg, err := secure.ClientTLS(certPath)
	if err != nil {
		t.Fatalf("конфигурация клиента не собрана: %v", err)
	}

	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}, Timeout: 5 * time.Second}
}

// redirectLocation запрашивает адрес редиректа без перехода по Location и возвращает заголовок.
func redirectLocation(t *testing.T, redirectAddr, target string) string {
	t.Helper()

	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	deadline := time.Now().Add(20 * time.Second)

	var lastErr error

	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + redirectAddr + target)
		if err == nil {
			_ = resp.Body.Close()

			if resp.StatusCode != http.StatusMovedPermanently {
				t.Fatalf("код редиректа %d, ожидали %d", resp.StatusCode, http.StatusMovedPermanently)
			}

			return resp.Header.Get("Location")
		}

		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("адрес редиректа не ответил: %v", lastErr)

	return ""
}

// assertNoSecretsInProcessOutput проверяет, что закрытый ключ не появился в стандартном потоке ошибок
// процесса и в файле логов, куда сервис дублирует записи.
func assertNoSecretsInProcessOutput(t *testing.T, stderr, logPath string, keyPEM []byte) {
	t.Helper()

	keyBody := make([]string, 0, 16)

	for line := range strings.SplitSeq(strings.TrimSpace(string(keyPEM)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") {
			continue
		}

		keyBody = append(keyBody, line)
	}

	texts := map[string]string{"стандартном потоке ошибок": stderr}

	logContent, err := os.ReadFile(logPath)
	switch {
	case err == nil:
		texts["файле логов"] = string(logContent)
	case os.IsNotExist(err):
		t.Log("файл логов не создан — проверка только по стандартному потоку ошибок")
	default:
		t.Errorf("файл логов не читается: %v", err)
	}

	for name, text := range texts {
		if strings.Contains(text, privateKeyMark) {
			t.Errorf("в %s появился PEM-заголовок закрытого ключа:\n%s", name, text)
		}

		for _, line := range keyBody {
			if strings.Contains(text, line) {
				t.Fatalf("в %s появилось тело закрытого ключа: %s", name, line)
			}
		}
	}
}
