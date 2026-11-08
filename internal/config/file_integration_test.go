package config

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
)

// Сквозная проверка: собранный сервер должен читать настройки из JSON-файла и поднимать
// TLS ровно так же, как по флагу -s. Каталог процесса — временный, поэтому относительные
// пути сертификатов, ключей и аудита остаются внутри теста.

// shorterProcess — запущенный процесс сервера с перехватом журнала.
type shorterProcess struct {
	stderr *lockedBuf
	done   chan error
}

// lockedBuf накапливает вывод процесса и безопасен для чтения во время записи.
type lockedBuf struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *lockedBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

// String возвращает накопленный вывод.
func (b *lockedBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// buildTarget возвращает путь, куда `go build -o` положит бинарник: на Windows это всегда
// имя с .exe, а запуск пути без расширения не работает — os/exec не дописывает его сам.
func buildTarget(dir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	return filepath.Join(dir, name)
}

// buildShorter собирает бинарник сервера во временный каталог теста.
func buildShorter(t *testing.T) string {
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

// freeShorterAddr возвращает заведомо свободный адрес на loopback.
func freeShorterAddr(t *testing.T) string {
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

// writeFile пишет фикстуру по указанному пути.
func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("фикстура %s не записана: %v", path, err)
	}
}

// shorterEnv собирает окружение дочернего процесса: переменные, которые решают исход теста,
// вычищаются, остальное наследуется. Полный набор нужен потому, что без системных переменных
// (на Windows — SystemRoot, PATHEXT и прочее) процесс может не запуститься вовсе.
func shorterEnv(values []string) []string {
	skip := make(map[string]bool, len(envNames))

	for _, name := range envNames {
		skip[name] = true
	}

	env := make([]string, 0, len(os.Environ())+len(values))

	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if skip[name] {
			continue
		}

		env = append(env, item)
	}

	return append(env, values...)
}

// startShorter запускает сервер с указанными аргументами и окружением. Переменные запуска
// не наследуются: они не должны решать исход теста.
func startShorter(t *testing.T, bin, dir string, args, env []string) *shorterProcess {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Stdout = io.Discard
	cmd.Env = shorterEnv(env)

	stderr := &lockedBuf{}
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

	t.Cleanup(func() {
		cancel()

		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("процесс сервера не завершился после отмены контекста")
		}
	})

	return &shorterProcess{stderr: stderr, done: done}
}

// waitHTTPS допрашивается до первого ответа по TLS любому кода.
func waitHTTPS(t *testing.T, addr string, proc *shorterProcess) *http.Response {
	t.Helper()

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}}, //nolint:gosec // самоподписанный материал теста
		Timeout:   2 * time.Second,
	}

	return waitAnswered(t, client, "https://"+addr+"/not-found", proc)
}

// waitHTTP допрашивается до первого ответа по обычному HTTP любому кода.
func waitHTTP(t *testing.T, addr string, proc *shorterProcess) *http.Response {
	t.Helper()

	return waitAnswered(t, &http.Client{Timeout: 2 * time.Second}, "http://"+addr+"/not-found", proc)
}

func waitAnswered(t *testing.T, client *http.Client, url string, proc *shorterProcess) *http.Response {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)

	var lastErr error

	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			return resp
		}

		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("по %s никто не ответил: %v\nвывод процесса:\n%s", url, lastErr, proc.stderr.String())

	return nil
}

// refusableClient не ходит по редиректам, чтобы увидеть сам ответ 301.
func refusableClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       2 * time.Second,
	}
}

// dialBlocked проверяет, что на адресе никто не слушает.
func dialBlocked(t *testing.T, addr string) bool {
	t.Helper()

	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return true
	}

	if err := conn.Close(); err != nil {
		t.Errorf("соединение не закрыто: %v", err)
	}

	return false
}

func TestServerReadsConfigFile(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildShorter(t)

	t.Run("файл задаёт адрес и включает HTTPS", func(t *testing.T) {
		dir := t.TempDir()
		tlsAddr := freeShorterAddr(t)
		redirectAddr := freeShorterAddr(t)

		path := filepath.Join(dir, "config.json")
		writeFile(t, path, `{
    "server_address": "`+tlsAddr+`",
    "http_server_address": "`+redirectAddr+`",
    "enable_https": true,
    "cert_file": "cert.pem",
    "key_file": "key.pem",
    "base_url": "http://localhost:8080/"
}`)

		proc := startShorter(t, bin, dir, []string{"-c", path}, nil)

		resp := waitHTTPS(t, tlsAddr, proc)
		defer func() { _ = resp.Body.Close() }()

		if resp.TLS == nil {
			t.Error("основной адрес отвечает без TLS, хотя enable_https включён файлом")
		}

		// Редирект обязан отвечать 301 на https:// — ровно как при -s.
		redirect, err := refusableClient().Get("http://" + redirectAddr + "/api/shorten")
		if err != nil {
			t.Fatalf("редирект не ответил: %v\nвывод процесса:\n%s", err, proc.stderr.String())
		}
		defer func() { _ = redirect.Body.Close() }()

		if redirect.StatusCode != http.StatusMovedPermanently {
			t.Errorf("редирект ответил %d, ожидали 301", redirect.StatusCode)
		}

		if !strings.HasPrefix(redirect.Header.Get("Location"), "https://") {
			t.Errorf("Location %q, ожидали адрес на https://", redirect.Header.Get("Location"))
		}

		if out := proc.stderr.String(); !strings.Contains(out, "Запуск HTTPS-сервера на "+tlsAddr) {
			t.Errorf("в журнале нет строки про TLS-слушателя на адресе из файла:\n%s", out)
		}
	})

	t.Run("переменная переопределяет адрес из файла", func(t *testing.T) {
		dir := t.TempDir()
		fileAddr := freeShorterAddr(t)
		envAddr := freeShorterAddr(t)

		path := filepath.Join(dir, "config.json")
		writeFile(t, path, `{"server_address": "`+fileAddr+`", "enable_https": true, "cert_file": "cert.pem", "key_file": "key.pem"}`)

		proc := startShorter(t, bin, dir, []string{"-c", path}, []string{"SERVER_ADDRESS=" + envAddr})

		resp := waitHTTPS(t, envAddr, proc)
		defer func() { _ = resp.Body.Close() }()

		if !dialBlocked(t, fileAddr) {
			t.Errorf("адрес из файла %s тоже слушается — переменная его не переопределила", fileAddr)
		}
	})

	t.Run("обычный HTTP без enable_https в файле", func(t *testing.T) {
		dir := t.TempDir()
		addr := freeShorterAddr(t)

		path := filepath.Join(dir, "config.json")
		writeFile(t, path, `{"server_address": "`+addr+`"}`)

		proc := startShorter(t, bin, dir, []string{"-c", path}, nil)

		resp := waitHTTP(t, addr, proc)
		defer func() { _ = resp.Body.Close() }()

		if resp.TLS != nil {
			t.Error("TLS включился без enable_https, -s и ENABLE_HTTPS")
		}
	})

	t.Run("битый файл не мешает флагу", func(t *testing.T) {
		dir := t.TempDir()
		addr := freeShorterAddr(t)

		path := filepath.Join(dir, "broken.json")
		writeFile(t, path, `{"server_address": `)

		proc := startShorter(t, bin, dir, []string{"-c", path, "-a", addr}, nil)

		resp := waitHTTP(t, addr, proc)
		defer func() { _ = resp.Body.Close() }()

		out := proc.stderr.String()
		if !strings.Contains(out, "конфигурация из файла не применена") || !strings.Contains(out, path) {
			t.Errorf("предупреждения с путём %q нет в журнале:\n%s", path, out)
		}
	})

	t.Run("неразбираемая переменная завершает до побочных эффектов", func(t *testing.T) {
		dir := t.TempDir()
		auditPath := filepath.Join(dir, "audit.log")

		path := filepath.Join(dir, "config.json")
		writeFile(t, path, `{"server_address": "`+freeShorterAddr(t)+`", "enable_https": true, "audit_file": "audit.log"}`)

		proc := startShorter(t, bin, dir, []string{"-c", path}, []string{"ENABLE_HTTPS=yes-please"})

		select {
		case err := <-proc.done:
			if err == nil {
				t.Error("сервер завершился успешно при неразбираемом ENABLE_HTTPS")
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("сервер не завершился:\n%s", proc.stderr.String())
		}

		if !strings.Contains(proc.stderr.String(), EnvEnableHTTPS) {
			t.Errorf("в выводе нет имени переменной:\n%s", proc.stderr.String())
		}

		if _, err := os.Stat(auditPath); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("файл аудита появился до ошибки конфигурации: %v", err)
		}
	})
}
